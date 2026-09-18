package vscreen

import (
	"context"
	"errors"
	"testing"
	"time"
)

type suspendLateDriver struct{ testDriver }

func (*suspendLateDriver) Quiesce(ctx context.Context, _ ResourceKey) error { <-ctx.Done(); return nil }

func TestActorSuspendDeadlineKeepsFrozen(t *testing.T) {
	for _, busy := range []bool{true, false} {
		t.Run(map[bool]string{true: "native-lock-busy", false: "late-nil-quiesce"}[busy], func(t *testing.T) {
			a, _, _ := fixture(t)
			display := a.Status().Display
			if busy {
				a.nativeMu.Lock()
				defer a.nativeMu.Unlock()
			} else {
				a.driver = &suspendLateDriver{}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if err := a.Suspend(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("suspend timeout returned %v", err)
			}
			if !a.Status().Frozen || a.Status().Display != display {
				t.Fatal("suspend lost frozen authority or display")
			}
		})
	}
}
