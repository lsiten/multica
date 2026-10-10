package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecutionClientNestedCallbacksUseClosedRoutes(t *testing.T) {
	identity := protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), WorkerID: uuid.NewString(), ExecutionID: uuid.NewString(), DispatchedAt: time.Now()}
	comment := uuid.NewString()
	token := "mwt_" + strings.Repeat("a", 64)
	paths := []string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong scoped credential")
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/claim") {
			_ = json.NewEncoder(w).Encode(TaskSupplement{CommentID: comment, Content: "one instruction"})
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	client, err := NewExecutionClient(server.URL, protocol.ExecutionGrantResponse{Token: token, ExpiresAt: time.Now().Add(time.Minute), Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	supplement, err := client.ClaimSupplement(t.Context())
	if err != nil || supplement.CommentID != comment {
		t.Fatal("claim failed")
	}
	if err = client.AckSupplement(t.Context(), comment, true, ""); err != nil {
		t.Fatal(err)
	}
	if err = client.RenewPrepareLease(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"worktree-delivery", "project-graph/events"} {
		if err = client.Callback(t.Context(), op, map[string]any{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	before := len(paths)
	if err = client.AckSupplement(context.Background(), "../other", true, ""); err == nil {
		t.Fatal("bad comment path accepted")
	}
	if err = client.Callback(t.Context(), "supplements/"+comment+"/ack", nil, nil); err == nil {
		t.Fatal("resource was accepted as grant operation")
	}
	if len(paths) != before {
		t.Fatal("invalid route reached network")
	}
	if paths[1] != "/api/daemon/tasks/"+identity.TaskID+"/supplements/"+comment+"/ack" || paths[2] != "/api/daemon/tasks/"+identity.TaskID+"/prepare-lease" {
		t.Fatalf("unexpected paths: %v", paths)
	}
}
func TestExecutionClientDoesNotReplayUnknownSupplementClaim(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		connection, _, _ := w.(http.Hijacker).Hijack()
		connection.Close()
	}))
	defer server.Close()
	identity := protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), WorkerID: uuid.NewString(), ExecutionID: uuid.NewString(), DispatchedAt: time.Now()}
	client, err := NewExecutionClient(server.URL, protocol.ExecutionGrantResponse{Token: "mwt_" + strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Minute), Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ClaimSupplement(t.Context()); err == nil || calls.Load() != 1 {
		t.Fatalf("unknown claim retried: calls=%d err=%v", calls.Load(), err)
	}
}

func TestExecutionClientReportAndPinUseScopedRoutes(t *testing.T) {
	identity := protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), WorkerID: uuid.NewString(), ExecutionID: uuid.NewString(), DispatchedAt: time.Now()}
	token := "mwt_" + strings.Repeat("c", 64)
	type recorded struct {
		path string
		auth string
		body []byte
	}
	var calls []recorded
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, recorded{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: raw})
		mu.Unlock()
		if r.URL.Path == "/api/daemon/tasks/"+identity.TaskID+"/messages" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewExecutionClient(server.URL, protocol.ExecutionGrantResponse{Token: token, ExpiresAt: time.Now().Add(time.Minute), Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.ReportTaskMessages(t.Context(), []TaskMessageData{{Seq: 1, Type: "text", Content: "hello"}}); err != nil {
		t.Fatalf("ReportTaskMessages: %v", err)
	}
	if err = client.PinTaskSession(t.Context(), "session-1", "/work"); err != nil {
		t.Fatalf("PinTaskSession: %v", err)
	}
	// An empty pin must not post: no network call for a no-op probe.
	before := len(calls)
	if err = client.PinTaskSession(t.Context(), "", ""); err != nil {
		t.Fatalf("empty PinTaskSession: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != before {
		t.Fatalf("empty pin posted, got %d calls (expected no-op): %v", len(calls), calls)
	}
	if got := calls[0]; got.path != "/api/daemon/tasks/"+identity.TaskID+"/messages" || got.auth != "Bearer "+token || string(got.body) != `{"messages":[{"seq":1,"type":"text","content":"hello","created_at":"0001-01-01T00:00:00Z"}]}` {
		t.Fatalf("report body/path/auth wrong: path=%s auth=%s body=%s", got.path, got.auth, got.body)
	}
	if got := calls[1]; got.path != "/api/daemon/tasks/"+identity.TaskID+"/session" || got.auth != "Bearer "+token || string(got.body) != `{"session_id":"session-1","work_dir":"/work"}` {
		t.Fatalf("pin body/path/auth wrong: path=%s auth=%s body=%s", got.path, got.auth, got.body)
	}
}

func TestExecutionClientBuildsDaemonFreeRunProviderSeams(t *testing.T) {
	identity := protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), WorkerID: uuid.NewString(), ExecutionID: uuid.NewString(), DispatchedAt: time.Now()}
	token := "mwt_" + strings.Repeat("d", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewExecutionClient(server.URL, protocol.ExecutionGrantResponse{Token: token, ExpiresAt: time.Now().Add(time.Minute), Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	// These are the exact runProviderExecution seam signatures; assigning the
	// adapted closures proves the per-execution task worker can build every
	// transport-backed seam from a scoped ExecutionClient without a *Daemon.
	var (
		reportTaskMessages func(context.Context, string, []TaskMessageData) error = func(ctx context.Context, taskID string, messages []TaskMessageData) error {
			return client.ReportTaskMessages(ctx, messages)
		}
		pinTaskSession func(context.Context, string, string, string) error = func(ctx context.Context, taskID, sessionID, workDir string) error {
			return client.PinTaskSession(ctx, sessionID, workDir)
		}
		claimSupplement func(context.Context, string) (*TaskSupplement, error)    = func(ctx context.Context, taskID string) (*TaskSupplement, error) { return client.ClaimSupplement(ctx) }
		ackSupplement   func(context.Context, string, string, bool, string) error = func(ctx context.Context, taskID, commentID string, delivered bool, reason string) error {
			return client.AckSupplement(ctx, commentID, delivered, reason)
		}
		subscribeSupplement func(string) (<-chan struct{}, func()) = workerSupplementSubscribe()
	)
	if reportTaskMessages == nil || pinTaskSession == nil || claimSupplement == nil || ackSupplement == nil || subscribeSupplement == nil {
		t.Fatal("a runProviderExecution seam did not resolve")
	}
	wakeup, unsubscribe := subscribeSupplement(identity.TaskID)
	if wakeup == nil || unsubscribe == nil {
		t.Fatal("subscribeSupplement must yield a channel and an unsubscribe")
	}
	unsubscribe()
}
