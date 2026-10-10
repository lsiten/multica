package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// ApplicationServiceIdentity comes only from a currently valid service grant.
type ApplicationServiceIdentity struct {
	TokenHash         string
	RuntimeID         string
	WorkspaceID       string
	DaemonID          string
	OwnerID           string
	MemberID          string
	ServiceInstanceID string
	Generation        int64
	Operation         string
	ExpiresAt         time.Time
}

type applicationServiceIdentityKey struct{}

// WithApplicationServiceIdentity carries a credential boundary into app transactions.
func WithApplicationServiceIdentity(ctx context.Context, identity ApplicationServiceIdentity) context.Context {
	return context.WithValue(ctx, applicationServiceIdentityKey{}, identity)
}

// ApplicationServiceIdentityFromContext distinguishes scoped children from legacy clients.
func ApplicationServiceIdentityFromContext(ctx context.Context) ApplicationServiceIdentity {
	identity, _ := ctx.Value(applicationServiceIdentityKey{}).(ApplicationServiceIdentity)
	return identity
}

// GenerateApplicationServiceGrant creates an opaque, independent child transport secret.
func GenerateApplicationServiceGrant() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("generate application service grant: %w", err)
	}
	return "mps_" + hex.EncodeToString(secret[:]), nil
}
