package middleware

import (
	"github.com/multica-ai/multica/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestViewerCredentialOnlyFromVerifiedJWT(t *testing.T) {
	// Given: client-supplied binding headers have no authority.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Viewer-Credential", "forged")
	if _, ok := ViewerCredentialFromContext(req.Context()); ok {
		t.Fatal("unverified binding")
	}
	token := generateToken(validClaims(), auth.JWTSecret())
	req.Header.Set("Authorization", "Bearer "+token)
	var got ViewerCredential
	// When: real auth validates the signed token.
	authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		got, ok = ViewerCredentialFromContext(r.Context())
		if !ok {
			t.Error("missing verified binding")
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
	// Then: the private context contains the verified subject, hash, and expiry.
	if got.UserID != "test-user-id" || got.Hash != auth.HashToken(token) || got.Kind != "jwt" || !got.ExpiresAt.After(time.Now()) {
		t.Fatalf("bad binding: %+v", got)
	}
}
