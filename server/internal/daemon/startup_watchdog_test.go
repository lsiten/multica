package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// silentStartupBackend never emits a message and reports "aborted" once its
// context is cancelled, mirroring how the real backends translate the
// watchdog's SIGKILL into a terminal result.
type silentStartupBackend struct {
	started atomic.Bool
}

func (b *silentStartupBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	b.started.Store(true)
	messages := make(chan agent.Message)
	results := make(chan agent.Result, 1)
	go func() {
		<-ctx.Done()
		results <- agent.Result{Status: "aborted"}
	}()
	return &agent.Session{Messages: messages, Result: results}, nil
}

func TestExecuteAndDrain_StartupWatchdogFailsSilentRun(t *testing.T) {
	t.Parallel()

	var reported atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct {
				Messages []struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				for _, m := range body.Messages {
					if m.Type == "error" && m.Content != "" {
						reported.Add(1)
					}
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	d := newRepoReadyTestDaemon(t, handler)
	d.cfg.AgentStartupTimeout = 60 * time.Millisecond
	d.cfg.AgentIdleWatchdog = 30 * time.Second
	d.cfg.AgentToolWatchdog = 30 * time.Second

	backend := &silentStartupBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, _, err := d.executeAndDrain(ctx, backend, "p", agent.ExecOptions{}, slog.Default(), "startup-silent", "", new(atomic.Int32))
	if err != nil {
		t.Fatalf("executeAndDrain() error = %v", err)
	}
	if result.Status != "startup_timeout" {
		t.Fatalf("status = %q, want startup_timeout", result.Status)
	}
	if result.Error == "" {
		t.Fatal("startup_timeout result carries no diagnosis text")
	}
	if reported.Load() == 0 {
		t.Fatal("expected at least one inline error diagnosis posted to the transcript")
	}
}

func TestExecuteAndDrain_StartupWatchdogDisarmsAfterFirstOutput(t *testing.T) {
	t.Parallel()

	d := newTestDaemon(t)
	d.cfg.AgentStartupTimeout = 50 * time.Millisecond
	d.cfg.AgentIdleWatchdog = 30 * time.Second
	d.cfg.AgentToolWatchdog = 30 * time.Second

	backend := &firstOutputBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, _, err := d.executeAndDrain(ctx, backend, "p", agent.ExecOptions{}, slog.Default(), "startup-talks", "", new(atomic.Int32))
	if err != nil {
		t.Fatalf("executeAndDrain() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed (startup watchdog must disarm after first output)", result.Status)
	}
}

// firstOutputBackend emits one text message shortly after launch, then
// completes — proving the startup watchdog disarms permanently instead of
// killing a healthy cold start.
type firstOutputBackend struct{}

func (b *firstOutputBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	messages := make(chan agent.Message, 1)
	results := make(chan agent.Result, 1)
	go func() {
		defer close(messages)
		select {
		case <-time.After(20 * time.Millisecond):
			messages <- agent.Message{Type: agent.MessageText, Content: "hello"}
		case <-ctx.Done():
			return
		}
		results <- agent.Result{Status: "completed", Output: "done"}
	}()
	return &agent.Session{Messages: messages, Result: results}, nil
}
