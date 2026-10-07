package applicationgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ConnectionGrant can resolve only one dependency while both bound instance generations remain active.
type ConnectionGrant struct {
	WorkspaceID      string `json:"workspace_id"`
	MemberID         string `json:"member_id"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceGeneration int64  `json:"source_generation"`
	TargetInstanceID string `json:"target_instance_id"`
	TargetGeneration int64  `json:"target_generation"`
	jwt.RegisteredClaims
}

func connectionSigningKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("multica-application-connection-v1"))
	return mac.Sum(nil)
}

// SignConnectionGrant creates a lifecycle-bound resolver credential, never a platform session.
func SignConnectionGrant(secret []byte, grant ConnectionGrant) (string, error) {
	grant.Audience = jwt.ClaimStrings{"multica-application-connection-v1"}
	grant.IssuedAt = jwt.NewNumericDate(time.Now())
	return jwt.NewWithClaims(jwt.SigningMethodHS256, grant).SignedString(connectionSigningKey(secret))
}

// ParseConnectionGrant validates the narrow audience and signature; current database facts provide revocation.
func ParseConnectionGrant(secret []byte, token string) (ConnectionGrant, error) {
	var grant ConnectionGrant
	parsed, err := jwt.ParseWithClaims(token, &grant, func(*jwt.Token) (any, error) { return connectionSigningKey(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("multica-application-connection-v1"))
	if err != nil || !parsed.Valid || grant.MemberID == "" || grant.SourceGeneration < 1 || grant.TargetGeneration < 1 {
		return grant, errors.New("application connection grant is invalid")
	}
	return grant, nil
}
