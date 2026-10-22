package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestWorkerRunOutlivesManagementRequestAndAdmitsOnlyOnce(t *testing.T) {
	s := newWorkerService(t)
	input := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{}}
	if _, err := s.mutate(t.Context(), bindRequest(runtimeproc.Request{}, input)); err != nil {
		t.Fatal(err)
	}
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	s.executeProvider = func(ctx context.Context, _ *ExecutionClient, _ *workerContext) workerRunResult {
		calls.Add(1)
		entered <- ctx
		<-release
		return workerRunResult{Ran: true, Status: "completed", Output: "genuine result"}
	}
	requestCtx, cancel := context.WithCancel(t.Context())
	if _, err := s.mutate(requestCtx, runtimeproc.Request{Operation: "worker.run"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	var runCtx context.Context
	select {
	case runCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider run not admitted")
	}
	if runCtx.Err() != nil {
		t.Fatal("management cancellation cancelled owned provider run")
	}
	if _, err := s.mutate(t.Context(), runtimeproc.Request{Operation: "worker.run"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate submission relaunched business")
	}
	close(release)
	if err := s.stopProviderRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := s.read(t.Context(), runtimeproc.Request{Operation: "worker.result"})
	if err != nil {
		t.Fatal(err)
	}
	var result workerResultStatus
	if err := json.Unmarshal(raw, &result); err != nil || result.Result == nil || result.Result.Output != "genuine result" {
		t.Fatalf("worker result lost: %s %v", raw, err)
	}
}

func TestWorkerRunLargeResultUsesValidatedPrivateArtifact(t *testing.T) {
	s := newWorkerService(t)
	input := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{}}
	if _, err := s.mutate(t.Context(), bindRequest(runtimeproc.Request{}, input)); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("large output", 32<<10)
	s.executeProvider = func(context.Context, *ExecutionClient, *workerContext) workerRunResult {
		return workerRunResult{Ran: true, Status: "completed", Output: want}
	}
	if _, err := s.mutate(t.Context(), runtimeproc.Request{Operation: "worker.run"}); err != nil {
		t.Fatal(err)
	}
	if err := s.stopProviderRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := s.read(t.Context(), runtimeproc.Request{Operation: "worker.result"})
	if err != nil {
		t.Fatal(err)
	}
	var status workerResultStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.Artifact == nil || status.Result != nil {
		t.Fatalf("large result not handed off through artifact: %s %v", raw, err)
	}
	body, artifactErr := runtimeproc.ReadReviewArtifact(runtimeproc.ReviewArtifactNamespace(s.bootstrap.Root), runtimeproc.ReviewArtifactName(status.Artifact.RequestID), status.Artifact.Hash, status.Artifact.Length)
	var result workerRunResult
	if artifactErr != nil || json.Unmarshal(body, &result) != nil || result.Output != want {
		t.Fatalf("large result lost in private handoff: %v", artifactErr)
	}
}

func TestWorkerMessagesPersistForOwnerReplayAfterControllerRestart(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Operation string `json:"operation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !strings.HasSuffix(r.URL.Path, "/executions/reports") || body.Operation != "messages" {
			t.Errorf("unexpected owner replay: %s %s", r.URL.Path, body.Operation)
		}
	}))
	defer backend.Close()
	s := newWorkerService(t)
	cfg := Config{ServerBaseURL: backend.URL, WorkspacesRoot: t.TempDir(), Profile: s.bootstrap.Identity.Scope.Profile, DaemonID: s.bootstrap.Identity.Scope.DaemonID}
	input := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: backend.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{Config: cfg}}
	if _, err := s.mutate(t.Context(), bindRequest(runtimeproc.Request{}, input)); err != nil {
		t.Fatal(err)
	}
	if err := s.client.ReportTaskMessages(t.Context(), []TaskMessageData{{Seq: 1, Type: "text", Content: "owned message"}}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("worker waited for backend instead of local durability")
	}
	restarted := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	restarted.accountID = s.bootstrap.Identity.Scope.Account
	restarted.terminalReports.bindAccount(restarted.accountID)
	if pending, delivered := restarted.replayPendingTerminalReports(t.Context(), nil); pending != 0 || delivered != 1 {
		t.Fatalf("worker data not recovered by owner: %d/%d", pending, delivered)
	}
}

func TestWorkerResultStorageRetriesWithoutRerunningProvider(t *testing.T) {
	s := newWorkerService(t)
	input := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{}}
	if _, err := s.mutate(t.Context(), bindRequest(runtimeproc.Request{}, input)); err != nil {
		t.Fatal(err)
	}
	namespace := runtimeproc.ReviewArtifactNamespace(s.bootstrap.Root)
	if err := os.WriteFile(namespace, []byte("storage temporarily unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.executeProvider = func(context.Context, *ExecutionClient, *workerContext) workerRunResult {
		calls.Add(1)
		return workerRunResult{Ran: true, Status: "completed", Output: "retained genuine result"}
	}
	if _, err := s.mutate(t.Context(), runtimeproc.Request{Operation: "worker.run"}); err != nil {
		t.Fatal(err)
	}
	if err := s.stopProviderRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(t.Context(), runtimeproc.Request{Operation: "worker.result"}); err == nil {
		t.Fatal("unpersisted outcome returned as durable")
	}
	if err := os.Remove(namespace); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.workPersistRetryAt = time.Time{}
	s.mu.Unlock()
	raw, err := s.read(t.Context(), runtimeproc.Request{Operation: "worker.result"})
	var status workerResultStatus
	if err != nil || json.Unmarshal(raw, &status) != nil || status.Result == nil || status.Result.Output != "retained genuine result" {
		t.Fatalf("result storage did not recover: %s %v", raw, err)
	}
	if calls.Load() != 1 {
		t.Fatal("storage retry relaunched provider")
	}
}

func TestWorkerUsageSurvivesParentFinalizationAndBudgetReason(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(t.Context(), nil)
	defer cancel(nil)
	worker := &workerRunResult{Status: "completed", Usage: []TaskUsageEntry{{Provider: "fixture", Model: "model", InputTokens: 10, OutputTokens: 5, CostUSDTicks: 7}}}
	result := projectWorkerResult(worker, nil)
	recordWorkerUsage(budget, worker.Usage)
	var err error
	finalizeTaskBudget(ctx, nil, budget, &result, &err)
	if len(result.Usage) != 1 || result.Usage[0].InputTokens != 10 || result.Usage[0].CostUSDTicks != 7 {
		t.Fatalf("parent discarded worker usage: %+v", result)
	}
	worker.BudgetExceeded, worker.TokenBudgetExceeded = true, true
	result = projectWorkerResult(worker, nil)
	if result.Status != "blocked" || result.FailureReason != "token_budget_exceeded" {
		t.Fatalf("worker budget converted into success: %+v", result)
	}
}

func TestWaitWorkerResultDoesNotRetryFatalArtifactErrors(t *testing.T) {
	started := time.Now()
	_, err := waitWorkerResult(t.Context(), func(context.Context) (workerResultStatus, error) {
		return workerResultStatus{}, errors.New("worker result artifact identity changed")
	})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("fatal artifact error was retried: %v", err)
	}
}
