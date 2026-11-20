package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestWorkerSupplementPrivateChannelWakesProviderWithoutPoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s := newWorkerService(t)
	s.lifetime = ctx
	service, err := runtimeproc.NewService(runtimeproc.Config{
		Bootstrap:        s.bootstrap,
		Capabilities:     []string{"worker.bind", "worker.run", "worker.supplement-wakeup"},
		ReadCapabilities: []string{"worker.result"},
		ReadHandler:      s.read,
		Handler:          s.mutate,
		Shutdown:         s.shutdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- service.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	var transport *runtimeproc.Client
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for transport == nil {
		record, readErr := runtimeproc.ReadRecord(s.bootstrap.Root, s.bootstrap.Identity)
		if readErr == nil && record.State == "ready" {
			transport, err = runtimeproc.NewClient(record)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("private worker channel did not become ready")
		case <-ticker.C:
		}
	}
	worker := &workerProcessClient{transport: transport, gate: make(chan struct{}, 1), wait: func(context.Context) error { return nil }}
	grant := validWorkerGrant()
	claimed := make(chan struct{}, 1)
	additional := make(chan string, 1)
	providerAccepted := make(chan string, 1)
	acked := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/claim") {
			select {
			case content := <-additional:
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(TaskSupplement{CommentID: "11111111-1111-4111-8111-111111111111", AuthorType: "agent", Content: content}); err != nil {
					t.Error(err)
				}
			default:
				w.WriteHeader(http.StatusNoContent)
				select {
				case claimed <- struct{}{}:
				default:
				}
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		acked <- struct{}{}
	}))
	defer backend.Close()
	session := &agent.Session{SupplementReady: func() bool { return true }, Supplement: func(_ context.Context, content string) error { providerAccepted <- content; return nil }}
	s.executeProvider = func(ctx context.Context, client *ExecutionClient, run *workerContext) workerRunResult {
		wakeup, unsubscribe := s.supplements.subscribe(grant.Identity.TaskID)
		defer unsubscribe()
		runTaskSupplementLoop(ctx, session, grant.Identity.TaskID, wakeup, slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(c context.Context, _ string) (*TaskSupplement, error) { return client.ClaimSupplement(c) },
			func(c context.Context, _, id string, accepted bool, reason string) error {
				return client.AckSupplement(c, id, accepted, reason)
			}, time.Hour, time.Hour)
		return workerRunResult{Ran: true, Status: "completed"}
	}
	d := &Daemon{}
	runDone := make(chan error, 1)
	go func() {
		_, err := d.runWorkerExecution(ctx, worker, workerRunInput{Grant: grant, CallbackURL: backend.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{}})
		runDone <- err
	}()
	t.Cleanup(func() { cancel(); <-runDone })
	select {
	case <-claimed:
	case <-ctx.Done():
		t.Fatal("provider never checked its input queue")
	}
	additional <- "peer answer"
	started := time.Now()
	d.taskSupplementSignals.notify(grant.Identity.TaskID)
	select {
	case <-providerAccepted:
	case <-ctx.Done():
		t.Fatal("private channel did not wake provider before hourly poll")
	}
	select {
	case <-acked:
	case <-ctx.Done():
		t.Fatal("provider acceptance was not acknowledged")
	}
	t.Logf("private IPC to provider acceptance: %s (poll interval: 1h)", time.Since(started))
}
