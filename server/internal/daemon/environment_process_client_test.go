package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// newEnvironmentClientForService builds an environmentProcessClient whose control
// channel talks to a real runtimeproc service without spawning a child process.
// Review handlers block until cancellation unless the test customizes the config.
func newEnvironmentClientForService(t *testing.T, customize ...func(*runtimeproc.Config)) (*environmentProcessClient, *runtimeproc.Service, chan struct{}) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "test", Account: "account", Profile: "profile", DaemonID: "daemon", Service: "environment"}, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	blockingReview := func(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
		if r.Operation == "environment.mutation_scope" {
			return environmentOperationResult(environmentMutationScope{Global: true}, nil)
		}
		if r.Operation != "review.run" && r.Operation != "review.read" {
			return json.RawMessage(`{}`), nil
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, nil
	}
	config := runtimeproc.Config{
		Bootstrap:        bootstrap,
		Capabilities:     []string{"review.run", "environment.bind"},
		ReadCapabilities: []string{"review.read", "environment.mutation_scope"},
		Handler:          blockingReview,
		ReadHandler:      blockingReview,
	}
	for _, change := range customize {
		change(&config)
	}
	service, err := runtimeproc.NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, serveCancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- service.Serve(serveCtx) }()
	t.Cleanup(func() {
		serveCancel()
		select {
		case <-serveDone:
		case <-time.After(4 * time.Second):
			t.Error("service did not join")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	var client *runtimeproc.Client
	for time.Now().Before(deadline) {
		client, err = runtimeproc.Open(t.Context(), bootstrap.Root, bootstrap.Identity)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("open client: %v", err)
	}
	environmentClient := &environmentProcessClient{
		process:       &runtimeproc.Process{Client: client},
		bootstrap:     bootstrap,
		gate:          make(chan struct{}, 1),
		confirmations: make(chan struct{}, 4),
	}
	environmentClient.startRecovery()
	t.Cleanup(environmentClient.stopRecovery)
	return environmentClient, service, started
}

func TestEnvironmentReviewCallCancelDoesNotFailClose(t *testing.T) {
	// Given
	c, _, started := newEnvironmentClientForService(t)

	// When
	for range 2 {
		callCtx, callCancel := context.WithCancel(t.Context())
		callErr := make(chan error, 1)
		go func() {
			_, err := c.routeReviewToChild(callCtx, protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "rt", Action: "read"})
			callErr <- err
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			callCancel()
			t.Fatal("review request did not reach the service")
		}
		callCancel()
		if err := <-callErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected caller-side cancellation, got %v", err)
		}
	}

	// Then
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatalf("readonly cancellation created %d pending operations", pending)
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Operations) != 0 {
		t.Fatalf("readonly cancellation created %d durable receipts", len(record.Operations))
	}
	if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); err != nil {
		t.Fatalf("readonly cancellation blocked a later mutation: %v", err)
	}
}

func TestEnvironmentMutationCancelRetainsUncertainOutcome(t *testing.T) {
	// Given
	c, _, started := newEnvironmentClientForService(t)
	callCtx, callCancel := context.WithCancel(t.Context())
	defer callCancel()
	callErr := make(chan error, 1)

	// When
	go func() {
		callErr <- c.mutation(callCtx, "review.run", protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "rt", Action: "merge"}, nil)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("mutation did not reach the service")
	}
	callCancel()

	// Then
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected caller-side cancellation, got %v", err)
	}
	c.mu.Lock()
	var requestID string
	for id, pending := range c.pending {
		if pending.Request.Operation == "review.run" {
			requestID = id
		}
	}
	c.mu.Unlock()
	if requestID == "" {
		t.Fatal("canceled mutation lost its pending request identity")
	}
	var uncertain *environmentUncertainOperation
	if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); !errors.As(err, &uncertain) {
		t.Fatalf("next ordinary mutation was not blocked: %v", err)
	}
	receipt, err := c.process.Client.QueryOperation(t.Context(), requestID)
	if err != nil || receipt.State != "pending" {
		t.Fatalf("canceled mutation receipt was retired: %+v, %v", receipt, err)
	}
}
