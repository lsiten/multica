package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

func TestReviewRecoveryCancelsWhileAnotherOperationHoldsLock(t *testing.T) {
	// Given an occupied review gate and an already disconnected request.
	if !localReviewOperations.TryLock() {
		t.Fatal("review gate unexpectedly occupied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finished := make(chan string, 1)
	joined := make(chan struct{})
	defer func() {
		localReviewOperations.Unlock()
		<-joined
	}()
	d := &Daemon{}
	// When recovery waits for the same gate.
	go func() {
		defer close(joined)
		result, _ := d.recoverRemoteMerge(ctx, worktreeReviewRequest{}, "")
		finished <- result.Error
	}()
	// Then cancellation returns without requiring the active operation to finish.
	select {
	case message := <-finished:
		if !strings.Contains(message, context.Canceled.Error()) {
			t.Fatalf("expected cancellation, got %q", message)
		}
	case <-time.After(time.Second):
		t.Error("cancelled recovery remains blocked on review gate")
	}
}

func TestReviewGateCancelledAcquisitionDoesNotConsumeCapacity(t *testing.T) {
	// Given an available gate and a cancelled reader.
	gate := reviewOperationGate{semaphore: semaphore.NewWeighted(1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When it tries to acquire the gate.
	if err := gate.Lock(ctx); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	// Then a live operation can still acquire the single slot.
	if !gate.TryLock() {
		t.Fatal("cancelled reader consumed capacity")
	}
	defer gate.Unlock()
	if gate.TryLock() {
		gate.Unlock()
		t.Fatal("gate allowed overlapping operations")
	}
}
