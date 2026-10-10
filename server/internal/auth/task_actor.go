package auth

import (
	"context"
	"time"
)

// TaskActor carries server-resolved credential facts, never actor headers supplied
// by a client. Bound is false only for an existing legacy token without a claim.
type TaskActor struct {
	TokenID      string
	TokenHash    string
	TaskID       string
	AgentID      string
	WorkspaceID  string
	UserID       string
	Bound        bool
	RuntimeID    string
	DispatchedAt time.Time
}
type taskActorKey struct{}
type trustedTaskActorKey struct{}

// WithTaskActor installs identity resolved from the opaque credential lookup.
func WithTaskActor(ctx context.Context, actor TaskActor) context.Context {
	ctx = context.WithValue(ctx, trustedTaskActorKey{}, false)
	return context.WithValue(ctx, taskActorKey{}, actor)
}

// TaskActorFromContext returns authenticated actor identity, if present.
func TaskActorFromContext(ctx context.Context) (TaskActor, bool) {
	actor, ok := ctx.Value(taskActorKey{}).(TaskActor)
	return actor, ok
}

// WithTrustedInternalTaskActor marks an explicit service-internal call. HTTP
// handlers still require an authenticated TaskActor; this is not a header mode.
func WithTrustedInternalTaskActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustedTaskActorKey{}, true)
}

// TrustedInternalTaskActor distinguishes explicit internal work from missing auth.
func TrustedInternalTaskActor(ctx context.Context) bool {
	trusted, _ := ctx.Value(trustedTaskActorKey{}).(bool)
	return trusted
}
