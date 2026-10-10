package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// ExecutionRequest carries the callback fence through the business transaction.
// Present is set for all daemon callbacks, including legacy callers without identity.
type ExecutionRequest struct {
	Present     bool
	ExecutionID string
	WorkerID    string
	GrantHash   string
	Operation   string
	// NormalizedFailureReason is server-set when /complete is classified as a failed result.
	NormalizedFailureReason string
}

type executionRequestKey struct{}

// WithExecutionRequest attaches an authenticated callback boundary to its context.
func WithExecutionRequest(ctx context.Context, request ExecutionRequest) context.Context {
	return context.WithValue(ctx, executionRequestKey{}, request)
}

// ExecutionRequestFromContext returns the callback boundary, if present.
func ExecutionRequestFromContext(ctx context.Context) ExecutionRequest {
	request, _ := ctx.Value(executionRequestKey{}).(ExecutionRequest)
	return request
}

// GenerateExecutionGrant creates a distinct credential that is never an account token.
func GenerateExecutionGrant() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("generate execution grant: %w", err)
	}
	return "mwt_" + hex.EncodeToString(secret[:]), nil
}
