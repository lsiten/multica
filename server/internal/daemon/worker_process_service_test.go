package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// validWorkerGrant returns an mwt_ grant with a complete, non-zero identity.
func validWorkerGrant() protocol.ExecutionGrantResponse {
	return protocol.ExecutionGrantResponse{
		Token:     "mwt_" + strings.Repeat("a", 64),
		ExpiresAt: time.Now().Add(time.Hour),
		Identity: protocol.ExecutionIdentity{
			TaskID:       uuid.NewString(),
			RuntimeID:    uuid.NewString(),
			WorkerID:     uuid.NewString(),
			ExecutionID:  uuid.NewString(),
			DispatchedAt: time.Now(),
		},
	}
}

// newWorkerService is the workerProcessService a control parent would launch.
func newWorkerService(t *testing.T) *workerProcessService {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend:  "http://127.0.0.1:1",
		Account:  "acct",
		Profile:  "profile",
		DaemonID: "daemon",
		Service:  "worker",
	}, "fixture-build")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return &workerProcessService{bootstrap: bootstrap, state: "starting"}
}

func bindRequest(rl runtimeproc.Request, in workerBindRequest) runtimeproc.Request {
	raw, _ := json.Marshal(in)
	rl.Operation = "worker.bind"
	rl.Payload = raw
	return rl
}

// TestWorkerLaunchAdmitsOnlyOnExactIdentityGrantAndAuth is the F3 core: a
// provider may be launched only on the exact server-returned identity, a valid
// scoped grant, a matching worker id and a current launch authorization.
func TestWorkerLaunchAdmitsOnlyOnExactIdentityGrantAndAuth(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	result, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in))
	if err != nil {
		t.Fatalf("bind admitted: %v", err)
	}
	var status workerStatus
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatalf("result: %v", err)
	}
	if !status.Launchable {
		t.Fatalf("expected launchable, got reason %q", status.ReasonCode)
	}
	if !status.HasClient {
		t.Fatalf("expected a scoped execution client to be constructed")
	}
	if status.ExecutionID != in.Grant.Identity.ExecutionID {
		t.Fatalf("execution id mismatch: %s", status.ExecutionID)
	}
}

// TestWorkerLaunchRefusesUncertainOrMissing is the fail-closed matrix: an
// uncertain ACK, a missing grant, a missing launch authorization, or a worker-id
// mismatch never admits a launch.
func TestWorkerLaunchRefusesUncertainOrMissing(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(*workerBindRequest)
	}{
		{"uncertain", WorkerAuthReasonUncertain, func(b *workerBindRequest) { b.Uncertain = true }},
		{"no launch authorization", WorkerAuthReasonNoLaunchAuth, func(b *workerBindRequest) { b.LaunchAuthorized = false }},
		{"worker id mismatch", WorkerAuthReasonWorkerMismatch, func(b *workerBindRequest) { b.WorkerIDMatchesBind = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWorkerService(t)
			in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
			tc.mutate(&in)
			result, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in))
			if err != nil {
				t.Fatalf("bind should not error: %v", err)
			}
			var status workerStatus
			if err := json.Unmarshal(result, &status); err != nil {
				t.Fatalf("result: %v", err)
			}
			if status.Launchable {
				t.Fatalf("expected refusal on %s", tc.name)
			}
			if status.ReasonCode != tc.reason {
				t.Fatalf("expected reason %q, got %q", tc.reason, status.ReasonCode)
			}
		})
	}
}

// TestWorkerLaunchRefusesInvalidGrant verifies a missing or invalid grant is a
// refusal, not a remaining-budget or completion promise.
func TestWorkerLaunchRefusesInvalidGrant(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	in.Grant.Token = "mul_account" // not an mwt_ worker credential
	result, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	var status workerStatus
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatalf("result: %v", err)
	}
	if status.Launchable || status.ReasonCode != WorkerAuthReasonNoGrant {
		t.Fatalf("expected grant_missing refusal, got launchable=%v reason=%q", status.Launchable, status.ReasonCode)
	}
	if status.HasClient {
		t.Fatalf("an invalid grant must not build a client")
	}
}

// TestWorkerNonLoopbackCallbackBuildsNoClient verifies a worker that would call a
// non-loopback host builds no client: the transport is not ready even though the
// authorization facts admit a launch, so a control parent must check both.
func TestWorkerNonLoopbackCallbackBuildsNoClient(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://10.0.0.5:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	result, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	var status workerStatus
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatalf("result: %v", err)
	}
	if status.HasClient {
		t.Fatalf("a non-loopback callback must not build a client")
	}
	if !status.Launchable {
		t.Fatalf("authorization gate is independent of transport readiness: reason %q", status.ReasonCode)
	}
}

// TestWorkerStatusReportsBoundDecisionWithoutChangingIt verifies a read does not
// alter the decision: a lost launch ACK must query the recorded decision, not
// relaunch.
func TestWorkerStatusReportsBoundDecisionWithoutChangingIt(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	out, err := s.read(context.Background(), runtimeproc.Request{Operation: "worker.status"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var status workerStatus
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !status.Launchable || status.ExecutionID != in.Grant.Identity.ExecutionID {
		t.Fatalf("read must report the recorded decision: %+v", status)
	}
}

// TestWorkerCancelReleasesCredential verifies cancel clears the bound grant and
// client so a stopped worker holds no live credential.
func TestWorkerCancelReleasesCredential(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.cancel"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	out, err := s.read(context.Background(), runtimeproc.Request{Operation: "worker.status"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var status workerStatus
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status.Launchable || status.HasClient || status.ExecutionID != "" {
		t.Fatalf("cancel must clear the bound decision: %+v", status)
	}
}

// TestWorkerAcquireCallsControlOverExecutionClient verifies worker.acquire is real
// work: it uses the scoped execution client to query control, confirming the
// execution is live before the worker would launch a provider.
func TestWorkerAcquireCallsControlOverExecutionClient(t *testing.T) {
	var gotToken string
	var gotPath string
	var gotCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCount++
		gotToken = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"running"}`))
	}))
	defer server.Close()

	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: server.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	out, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.acquire"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if gotCount != 1 {
		t.Fatalf("expected exactly one control callback, got %d", gotCount)
	}
	if gotToken != "Bearer mwt_"+strings.Repeat("a", 64) {
		t.Fatalf("acquire must use the scoped mwt_ credential, got %q", gotToken)
	}
	if gotPath != "/api/daemon/tasks/"+in.Grant.Identity.TaskID+"/status" {
		t.Fatalf("acquire must query the execution status path, got %q", gotPath)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if result["acquired"] != true || result["state"] != "running" {
		t.Fatalf("acquire result unexpected: %+v", result)
	}
}

// TestWorkerAcquireRefusesWhenUnbound verifies acquire is a fail-closed action:
// a worker with no bound client cannot acquire and must not call control.
func TestWorkerAcquireRefusesWhenUnbound(t *testing.T) {
	s := newWorkerService(t)
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.acquire"}); err == nil {
		t.Fatalf("acquire without a bound client must be rejected")
	}
}

// TestWorkerReportCallsControlOverExecutionClient verifies worker.report produces a
// terminal result to control through the scoped execution client, using the mwt_
// credential on the exact operation path.
func TestWorkerReportCallsControlOverExecutionClient(t *testing.T) {
	var gotToken, gotPath string
	var gotCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCount++
		gotToken = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: server.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// report a usage result (a registered execution operation)
	reportPayload, _ := json.Marshal(workerReportRequest{Operation: "usage", Payload: json.RawMessage(`{}`)})
	req := runtimeproc.Request{Operation: "worker.report", Payload: reportPayload}
	if _, err := s.mutate(context.Background(), req); err != nil {
		t.Fatalf("report: %v", err)
	}
	if gotCount != 1 {
		t.Fatalf("expected exactly one control callback, got %d", gotCount)
	}
	if gotToken != "Bearer mwt_"+strings.Repeat("a", 64) {
		t.Fatalf("report must use the scoped mwt_ credential, got %q", gotToken)
	}
	if gotPath != "/api/daemon/tasks/"+in.Grant.Identity.TaskID+"/usage" {
		t.Fatalf("report must hit the exact operation path, got %q", gotPath)
	}
}

// TestWorkerReportRefusesWhenNotLaunchable verifies a worker that is not launchable
// never produced a provider run, so it must not produce a result the server would
// record as real work.
func TestWorkerReportRefusesWhenNotLaunchable(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: false}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	reportPayload, _ := json.Marshal(workerReportRequest{Operation: "usage", Payload: json.RawMessage(`{}`)})
	req := runtimeproc.Request{Operation: "worker.report", Payload: reportPayload}
	if _, err := s.mutate(context.Background(), req); err == nil {
		t.Fatalf("report must be refused when the worker is not launchable")
	}
}

// TestWorkerReportRefusesWhenUnbound verifies report is a fail-closed action.
func TestWorkerReportRefusesWhenUnbound(t *testing.T) {
	s := newWorkerService(t)
	reportPayload, _ := json.Marshal(workerReportRequest{Operation: "usage", Payload: json.RawMessage(`{}`)})
	req := runtimeproc.Request{Operation: "worker.report", Payload: reportPayload}
	if _, err := s.mutate(context.Background(), req); err == nil {
		t.Fatalf("report without a bound client must be rejected")
	}
}

// TestWorkerHonorCancelStopsOnTerminal verifies honor-cancel stops the worker when
// control reports a terminal task state (completed/failed/cancelled), and releases
// the bound credential so the worker cannot relaunch or report a fake result.
func TestWorkerHonorCancelStopsOnTerminal(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"state":"` + state + `"}`))
			}))
			defer server.Close()
			s := newWorkerService(t)
			in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: server.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true}
			if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
				t.Fatalf("bind: %v", err)
			}
			out, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.honor-cancel"})
			if err != nil {
				t.Fatalf("honor-cancel: %v", err)
			}
			var cancel workerCancelState
			if err := json.Unmarshal(out, &cancel); err != nil {
				t.Fatalf("result: %v", err)
			}
			if !cancel.Cancelled {
				t.Fatalf("terminal %q must cancel the worker", state)
			}
			// after a cancellation the worker must release its credential
			st, _ := s.read(context.Background(), runtimeproc.Request{Operation: "worker.status"})
			var status workerStatus
			json.Unmarshal(st, &status)
			if status.HasClient {
				t.Fatalf("a cancelled worker must release its client")
			}
		})
	}
}

// TestWorkerHonorCancelNotTerminal verifies a non-terminal (running) task state
// does not cancel the worker: the provider keeps running.
func TestWorkerHonorCancelNotTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"running"}`))
	}))
	defer server.Close()
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: server.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	out, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.honor-cancel"})
	if err != nil {
		t.Fatalf("honor-cancel: %v", err)
	}
	var cancel workerCancelState
	json.Unmarshal(out, &cancel)
	if cancel.Cancelled {
		t.Fatalf("a running task must not cancel the worker")
	}
}

// TestWorkerHonorCancelUncertainQueryIsNeverACancel verifies a failed query is
// never a cancellation: a flaky link must not stop an in-flight provider.
func TestWorkerHonorCancelUncertainQueryIsNeverACancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: server.URL, WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.honor-cancel"}); err == nil {
		t.Fatalf("an uncertain query must not be treated as a cancellation")
	}
	// the worker keeps its client (the execution is still live)
	st, _ := s.read(context.Background(), runtimeproc.Request{Operation: "worker.status"})
	var status workerStatus
	json.Unmarshal(st, &status)
	if !status.HasClient {
		t.Fatalf("an uncertain query must not release the client")
	}
}

// TestWorkerHonorCancelRefusesWhenUnbound verifies honor-cancel is fail-closed.
func TestWorkerHonorCancelRefusesWhenUnbound(t *testing.T) {
	s := newWorkerService(t)
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.honor-cancel"}); err == nil {
		t.Fatalf("honor-cancel without a bound client must be rejected")
	}
}

// TestWorkerGateRecordSatisfied verifies the worker-owned completion gate accepts a
// satisfied verification: the worker records it and the gate reports verified.
func TestWorkerGateRecordSatisfied(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	rec, _ := json.Marshal(workerGateRecord{Goal: "g", Criteria: []string{"c"}, Evidence: []string{"e"}, Verdict: "satisfied", Mode: "m"})
	out, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.gate-record", Payload: rec})
	if err != nil {
		t.Fatalf("gate-record: %v", err)
	}
	var v completionVerification
	json.Unmarshal(out, &v)
	if !v.Verified {
		t.Fatalf("satisfied verification must be accepted, got %+v", v)
	}
	// gate-status read must reflect the recorded gate
	st, _ := s.read(context.Background(), runtimeproc.Request{Operation: "worker.gate-status"})
	var sv completionVerification
	json.Unmarshal(st, &sv)
	if !sv.Verified {
		t.Fatalf("gate-status must report the recorded gate, got %+v", sv)
	}
}

// TestWorkerGateRecordUnsatisfied verifies an unsatisfied verification does not set
// the gate to verified (the worker may not claim completion without evidence).
func TestWorkerGateRecordUnsatisfied(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	rec, _ := json.Marshal(workerGateRecord{Goal: "g", Criteria: []string{"c"}, Evidence: []string{"e"}, Verdict: "failed"})
	out, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.gate-record", Payload: rec})
	if err != nil {
		t.Fatalf("gate-record: %v", err)
	}
	var v completionVerification
	json.Unmarshal(out, &v)
	if v.Verified {
		t.Fatalf("an unsatisfied verification must not set the gate to verified")
	}
}

// TestWorkerGateRecordRefusesWhenUnbound verifies gate-record is fail-closed: a
// worker with no bound gate cannot record a verification.
func TestWorkerGateRecordRefusesWhenUnbound(t *testing.T) {
	s := newWorkerService(t)
	rec, _ := json.Marshal(workerGateRecord{Goal: "g", Criteria: []string{"c"}, Evidence: []string{"e"}, Verdict: "satisfied"})
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.gate-record", Payload: rec}); err == nil {
		t.Fatalf("gate-record without a bound gate must be rejected")
	}
}

// TestWorkerGateStatusUnbound reports the gate unavailable, not an error.
func TestWorkerGateStatusUnbound(t *testing.T) {
	s := newWorkerService(t)
	out, err := s.read(context.Background(), runtimeproc.Request{Operation: "worker.gate-status"})
	if err != nil {
		t.Fatalf("gate-status: %v", err)
	}
	var v completionVerification
	json.Unmarshal(out, &v)
	if v.Verified || v.Reason == "" {
		t.Fatalf("gate-status for an unbound worker must report unavailable, got %+v", v)
	}
}

// TestWorkerCancelResetsGate verifies cancel releases the bound gate so a later
// bind starts a fresh gate rather than inheriting a prior verification.
func TestWorkerCancelResetsGate(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	rec, _ := json.Marshal(workerGateRecord{Goal: "g", Criteria: []string{"c"}, Evidence: []string{"e"}, Verdict: "satisfied"})
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.gate-record", Payload: rec}); err != nil {
		t.Fatalf("gate-record: %v", err)
	}
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.cancel"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	out, err := s.read(context.Background(), runtimeproc.Request{Operation: "worker.gate-status"})
	if err != nil {
		t.Fatalf("gate-status: %v", err)
	}
	var v completionVerification
	json.Unmarshal(out, &v)
	if v.Reason != "completion_verifier_unavailable" {
		t.Fatalf("cancel must reset the gate, got %+v", v)
	}
}

// TestWorkerUnknownOperationRejects verifies the closed operation set.
func TestWorkerUnknownOperationRejects(t *testing.T) {
	s := newWorkerService(t)
	if _, err := s.mutate(context.Background(), runtimeproc.Request{Operation: "worker.bogus"}); err == nil {
		t.Fatalf("unknown operation must be rejected")
	}
}

// workerGenuineRunBackend is a fake agent.Backend for the F3 worker genuine-run
// test: it emits one transcript message then a completed result so
// executePreparedProvider drives the real runProviderExecution drain loop (not a
// fabricated success). It records that Execute was actually invoked.
type workerGenuineRunBackend struct {
	started atomic.Bool
}

func (b *workerGenuineRunBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	b.started.Store(true)
	msgCh := make(chan agent.Message, 1)
	resCh := make(chan agent.Result, 1)
	go func() {
		msgCh <- agent.Message{Type: agent.MessageText, Content: "worker ran the provider"}
		close(msgCh)
		resCh <- agent.Result{Status: "completed", Output: "done from worker"}
	}()
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

// TestExecutePreparedProviderGenuinelyRunsProvider is the F3 core: the per-
// execution task worker genuinely runs the provider through the shared
// *Daemon-free runProviderExecution (not a fabricated success). A real phase
// recorder is supplied (runProviderExecution dereferences it); nil budget is
// tolerated. The emitted transcript message is reported through the wired seam
// and the completed result is produced, proving the run actually happened.
func TestExecutePreparedProviderGenuinelyRunsProvider(t *testing.T) {
	backend := &workerGenuineRunBackend{}
	var mu sync.Mutex
	var reported []TaskMessageData
	reportFn := func(ctx context.Context, taskID string, messages []TaskMessageData) error {
		mu.Lock()
		reported = append(reported, messages...)
		mu.Unlock()
		return nil
	}
	pinFn := func(ctx context.Context, taskID, sessionID, workDir string) error { return nil }
	subscribe := func(taskID string) (<-chan struct{}, func()) { return make(chan struct{}), func() {} }
	claimFn := func(ctx context.Context, taskID string) (*TaskSupplement, error) { return nil, nil }
	ackFn := func(ctx context.Context, taskID, commentID string, delivered bool, reason string) error { return nil }
	phaseRecorder := newTaskPhaseRecorder(slog.Default(), time.Now)
	var msgSeq atomic.Int32

	result, tools, err := executePreparedProvider(
		context.Background(), backend, "PROMPT", agent.ExecOptions{}, slog.Default(), "task-run", "", &msgSeq, nil, phaseRecorder,
		reportFn, pinFn, subscribe, claimFn, ackFn,
		defaultTaskSupplementReadyInterval, defaultTaskSupplementPollInterval, 0, 0, 0,
	)
	if err != nil {
		t.Fatalf("executePreparedProvider: %v", err)
	}
	if !backend.started.Load() {
		t.Fatalf("backend.Execute was never called; the worker did not genuinely run the provider")
	}
	if result.Status != "completed" || result.Output != "done from worker" {
		t.Fatalf("unexpected result: %+v (want completed/done from worker)", result)
	}
	mu.Lock()
	defer mu.Unlock()
	var sawText bool
	for _, m := range reported {
		if m.Content == "worker ran the provider" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("transcript message was not drained/reported; the provider run was not genuine: %v", reported)
	}
	_ = tools
}

// TestWorkerRunFailsClosedWithoutContextOrClient is the fail-closed matrix for
// the worker.run handler: without a bound context the worker refuses to run a
// provider rather than fabricating one.
func TestWorkerRunFailsClosedWithoutContextOrClient(t *testing.T) {
	s := newWorkerService(t)
	in := workerBindRequest{Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true}
	// A bind WITHOUT a context: launchable but no context to run from.
	if _, err := s.mutate(context.Background(), bindRequest(runtimeproc.Request{}, in)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	req := runtimeproc.Request{Operation: "worker.run"}
	req.Payload = []byte(`{}`)
	_, rerr := s.mutate(context.Background(), req)
	if rerr == nil {
		t.Fatalf("worker.run without a context must fail closed")
	}
	if rerr.Code != "no_context" {
		t.Fatalf("expected no_context fail-closed, got %s: %v", rerr.Code, rerr)
	}
}
