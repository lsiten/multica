package applicationgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func accessSigningKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("multica-application-access-v1"))
	return mac.Sum(nil)
}

// AccessClaims are scoped to one endpoint revision, never a Multica management session.
type AccessClaims struct {
	EndpointID       string `json:"endpoint_id"`
	WorkspaceID      string `json:"workspace_id"`
	MemberID         string `json:"member_id"`
	Revision         int64  `json:"revision"`
	SourceInstanceID string `json:"source_instance_id,omitempty"`
	SourceGeneration int64  `json:"source_generation,omitempty"`
	TargetGeneration int64  `json:"target_generation,omitempty"`
	jwt.RegisteredClaims
}

// Origin validates a dedicated application domain. Local development may use HTTP localhost names.
func Origin(raw string) (*url.URL, error) {
	origin, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, errors.New("application origin must be an absolute HTTP(S) origin without a path")
	}
	if origin.Scheme != "https" && origin.Hostname() != "localhost" && !strings.HasSuffix(origin.Hostname(), ".localhost") {
		return nil, errors.New("application origin requires HTTPS outside localhost development")
	}
	origin.Path = ""
	return origin, nil
}

// EndpointOrigin gives every application endpoint its own browser origin.
func EndpointOrigin(base *url.URL, id string) *url.URL {
	origin := *base
	origin.Host = id + "." + base.Host
	return &origin
}

// SignAccess signs a bounded audience distinct from the platform's normal JWTs.
func SignAccess(secret []byte, endpointID, workspaceID, userID, memberID string, revision int64) (string, error) {
	claims := AccessClaims{EndpointID: endpointID, WorkspaceID: workspaceID, MemberID: memberID, Revision: revision, RegisteredClaims: jwt.RegisteredClaims{Subject: userID, Audience: jwt.ClaimStrings{"multica-application-access-v1"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(accessSigningKey(secret))
}

// SignConnectionAccess scopes a short service session to the source and target instance generations.
func SignConnectionAccess(secret []byte, endpointID string, revision int64, grant ConnectionGrant) (string, error) {
	claims := AccessClaims{EndpointID: endpointID, WorkspaceID: grant.WorkspaceID, MemberID: grant.MemberID, Revision: revision, SourceInstanceID: grant.SourceInstanceID, SourceGeneration: grant.SourceGeneration, TargetGeneration: grant.TargetGeneration, RegisteredClaims: jwt.RegisteredClaims{Subject: grant.Subject, Audience: jwt.ClaimStrings{"multica-application-access-v1"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(accessSigningKey(secret))
}

// ParseAccess refuses platform, expired and incorrectly scoped signing methods.
func ParseAccess(secret []byte, token string) (AccessClaims, error) {
	var claims AccessClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(token *jwt.Token) (any, error) { return accessSigningKey(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("multica-application-access-v1"), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid || claims.MemberID == "" {
		return claims, errors.New("application access session is invalid or expired")
	}
	return claims, nil
}
