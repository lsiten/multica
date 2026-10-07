package applicationgateway

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestApplicationAccessCannotBeUsedAsPlatformOrAnotherEndpointSession(t *testing.T) {
	secret := []byte("fixture-secret-that-is-not-a-real-account-key")
	token, err := SignAccess(secret, "endpoint", "workspace", "user", "membership", 7)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAccess(secret, token)
	if err != nil || claims.EndpointID != "endpoint" || claims.Revision != 7 || claims.MemberID != "membership" {
		t.Fatalf("scoped claims=%+v err=%v", claims, err)
	}
	platform, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseAccess(secret, platform); err == nil {
		t.Fatal("platform token became an application session")
	}
	if parsed, err := jwt.Parse(token, func(token *jwt.Token) (any, error) { return secret, nil }); err == nil || parsed.Valid {
		t.Fatal("application token became a platform session")
	}
	if _, err = ParseAccess([]byte("wrong-key"), token); err == nil {
		t.Fatal("untrusted signature accepted")
	}
}

func TestApplicationAccessRejectsSessionsWithoutMembershipIdentity(t *testing.T) {
	secret := []byte("membership-fixture-secret-not-a-platform-account")
	token, err := SignAccess(secret, "endpoint", "workspace", "user", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAccess(secret, token); err == nil {
		t.Fatal("unbound session bypassed membership revocation")
	}
}

func TestApplicationOriginsRequireDedicatedSecureHostNames(t *testing.T) {
	for _, raw := range []string{"http://apps.example.test", "https://user:secret@apps.example.test", "https://apps.example.test/path", "https://apps.example.test?token=value"} {
		if _, err := Origin(raw); err == nil {
			t.Fatalf("invalid origin accepted: %s", raw)
		}
	}
	for _, raw := range []string{"https://apps.example.test", "http://apps.localhost:18608"} {
		origin, err := Origin(raw)
		if err != nil {
			t.Fatal(err)
		}
		if EndpointOrigin(origin, "application").Hostname() != "application."+origin.Hostname() {
			t.Fatal("endpoint did not receive an isolated origin")
		}
	}
}
