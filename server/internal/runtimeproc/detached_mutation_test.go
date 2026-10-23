package runtimeproc

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDetachedMutationCompletesAfterDisconnect(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	b := testBootstrap(t)
	_, c, _, _ := startService(t, Config{Bootstrap: b, DetachMutations: true, Capabilities: []string{"work"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		close(returned)
		return json.RawMessage(`{"done":true}`), nil
	}})
	req := request(t, c, "work", "")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, req); done <- err }()
	<-entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("client did not disconnect")
	}
	select {
	case <-returned:
		t.Fatal("disconnect canceled durable work")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		receipt, err := c.QueryOperation(t.Context(), req.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.State == "completed" {
			disk, err := ReadRecord(b.Root, b.Identity)
			if err != nil || disk.Operations[req.RequestID].State != "completed" {
				t.Fatalf("disk=%+v %v", disk, err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("detached mutation did not complete")
}

func TestDetachedMutationCancellationRetainsPending(t *testing.T) {
	for _, mode := range []string{"deadline", "root"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			b := testBootstrap(t)
			s, c, cancel, _ := startService(t, Config{Bootstrap: b, DetachMutations: true, Capabilities: []string{"work"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
				close(entered)
				<-ctx.Done()
				return nil, nil
			}})
			req := request(t, c, "work", "")
			if mode == "deadline" {
				req.Deadline = time.Now().Add(70 * time.Millisecond)
			}
			done := make(chan struct{})
			go func() { _, _ = c.Call(t.Context(), req); close(done) }()
			<-entered
			if mode == "root" {
				cancel()
			}
			<-done
			s.mu.Lock()
			receipt := s.record.Operations[req.RequestID]
			s.mu.Unlock()
			if receipt.State != "pending" {
				t.Fatalf("canceled outcome=%+v", receipt)
			}
		})
	}
}

func TestDetachedMutationQueuedCancellationCreatesNoIntent(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), DetachMutations: true, Capabilities: []string{"work"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}})
	first := request(t, c, "work", "")
	finished := make(chan struct{})
	go func() { _, _ = c.Call(t.Context(), first); close(finished) }()
	<-entered
	second := request(t, c, "work", "")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, second); err == nil {
		t.Fatal("queued caller was not canceled")
	}
	close(release)
	<-finished
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.record.Operations[second.RequestID]; ok {
		t.Fatal("queued cancellation left intent")
	}
}
