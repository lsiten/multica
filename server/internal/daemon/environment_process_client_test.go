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
// channel talks to a real runtimeproc service, so mutation's call path is
// exercised without a spawned child process. The review.run handler blocks until
// the caller cancels, so a caller-side cancellation is observed deterministically
// on the control channel.
func newEnvironmentClientForService(t *testing.T) (*environmentProcessClient, *runtimeproc.Service, chan struct{}) {
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
	service, err := runtimeproc.NewService(runtimeproc.Config{
		Bootstrap: bootstrap,
		Capabilities: []string{"review.run"},
		Handler: func(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			if r.Operation != "review.run" {
				return json.RawMessage(`{}`), nil
			}
			select {
			case started <- struct{}{}:
			default:
			}
			// Block the call so a caller-side cancellation is observed on the
			// control channel, mirroring a slow or aborted local read.
			select {
			case <-ctx.Done():
				return nil, nil
			case <-time.After(10 * time.Second):
			}
			return json.RawMessage(`{}`), nil
		},
	})
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
	return &environmentProcessClient{
		process:       &runtimeproc.Process{Client: client},
		bootstrap:     bootstrap,
		gate:          make(chan struct{}, 1),
		confirmations: make(chan struct{}, 4),
	}, service, started
}

// TestEnvironmentReviewCallCancelDoesNotFailClose verifies that a caller-side
// cancellation on a review call is a clean abort, not an uncertain outcome: the
// client must not be fail-closed so a later review read still proceeds instead
// of erroring with "environment review.run outcome unknown ... context canceled".
func TestEnvironmentReviewCallCancelDoesNotFailClose(t *testing.T) {
	c, _, started := newEnvironmentClientForService(t)

	callCtx, callCancel := context.WithCancel(t.Context())
	defer callCancel()
	callErr := make(chan error, 1)
	go func() { callErr <- c.mutation(callCtx, "review.run", protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "rt", Action: "read"}, nil) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("review request did not reach the service")
	}
	callCancel()
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected caller-side cancellation, got %v", err)
	}
	if c.uncertain != nil {
		t.Fatalf("caller-side cancellation fail-closed the client: %v", c.uncertain)
	}

	// A later mutation on the same client must not be blocked by the stale abort:
	// it proceeds past the uncertain gate (Health + call) rather than returning
	// the fail-closed outcome immediately.
	followCtx, followCancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer followCancel()
	followErr := c.mutation(followCtx, "review.run", protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "rt", Action: "read"}, nil)
	if errors.Is(followErr, context.DeadlineExceeded) {
		t.Log("follow-up proceeded (Health ok, call timed out) rather than failing-closed")
	}
	var uncertain *environmentUncertainOperation
	if errors.As(followErr, &uncertain) {
		t.Fatalf("follow-up mutation failed-closed despite clean abort: %v", followErr)
	}
	if c.uncertain != nil {
		t.Fatalf("client remained fail-closed: %v", c.uncertain)
	}
}
