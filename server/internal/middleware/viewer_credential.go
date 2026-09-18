package middleware

import (
	"context"
	"time"
)

type viewerCredentialKey struct{}

// ViewerCredential describes already-verified human credentials without retaining a bearer token.
type ViewerCredential struct {
	Kind      string
	Hash      string
	UserID    string
	ExpiresAt time.Time
}

func withViewerCredential(ctx context.Context, binding ViewerCredential) context.Context {
	return context.WithValue(ctx, viewerCredentialKey{}, binding)
}

// ViewerCredentialFromContext is populated only after successful JWT or local PAT verification.
func ViewerCredentialFromContext(ctx context.Context) (ViewerCredential, bool) {
	binding, ok := ctx.Value(viewerCredentialKey{}).(ViewerCredential)
	return binding, ok
}
