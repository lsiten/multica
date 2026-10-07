package applicationgateway

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestApplicationConnectionGrantCannotAuthenticateAsPlatformOrServiceSession(t *testing.T) {
	secret := []byte("connection-fixture-secret-not-a-platform-account")
	grant := ConnectionGrant{WorkspaceID: "workspace", MemberID: "membership", SourceInstanceID: "source", SourceGeneration: 2, TargetInstanceID: "target", TargetGeneration: 3, RegisteredClaims: jwt.RegisteredClaims{Subject: "member"}}
	token, err := SignConnectionGrant(secret, grant)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseConnectionGrant(secret, token)
	if err != nil || parsed.SourceInstanceID != "source" || parsed.TargetGeneration != 3 || parsed.MemberID != "membership" {
		t.Fatalf("grant scope=%+v error=%v", parsed, err)
	}
	if session, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return secret, nil }); err == nil || session.Valid {
		t.Fatal("connection grant became a platform session")
	}
	if _, err := ParseAccess(secret, token); err == nil {
		t.Fatal("resolver grant became a target service session")
	}
	if _, err := ParseConnectionGrant([]byte("other-key"), token); err == nil {
		t.Fatal("untrusted grant signature accepted")
	}
}

func TestApplicationConnectionGrantRejectsMissingMembershipIdentity(t *testing.T) {
	secret := []byte("membership-fixture-secret-not-a-platform-account")
	token, err := SignConnectionGrant(secret, ConnectionGrant{WorkspaceID: "workspace", SourceInstanceID: "source", SourceGeneration: 1, TargetInstanceID: "target", TargetGeneration: 1, RegisteredClaims: jwt.RegisteredClaims{Subject: "member"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConnectionGrant(secret, token); err == nil {
		t.Fatal("unbound dependency grant bypassed membership revocation")
	}
}
