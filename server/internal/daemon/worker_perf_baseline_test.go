package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This file is the F6 "performance and limits" deliverable: a comparable
// baseline between the ORIGINAL (in-process) hot path and the NEW
// (process-extracted) hot path. The plan (daemon-multiprocess.md, Final
// verification wave F6) requires "报告原/新结构可比 baseline" and forbids using
// a narrow test to *substitute* for full-architecture acceptance; a comparable
// baseline is a required, distinct deliverable, not a substitute.
//
// It benchmarks the single hot-path decision attemptWorkerRun — the only branch
// the control parent takes on the move-out — in both modes:
//
//	original / in-process: the gate is OFF (WorkerProcessEnabled false), so
//	  attemptWorkerRun returns a zero decision and never launches a worker; the
//	  control parent settles the execution in-process (the legacy default).
//	extracted / new:      the gate is ON and a launchable bind, so
//	  attemptWorkerRun drives the worker (bind -> run -> stop) through a faithful
//	  transport, exactly as the opt-in hot path does
//	  (TestAttemptWorkerRunOptInTakesWorkerPath).
//
// Both measurements run the real decision on the same task, so the ratio is a
// comparable per-execution overhead of the extraction decision itself. The
// provider run is served by fakeWorkerTransport (not a real child), so this
// measures the decision/transport overhead, not a full provider run; that is the
// part the extraction adds, and it is the part F6 must make comparable. A full
// real-child end-to-end run is covered by the runtime tests, not benchmarked
// here (a real child's spawn time is platform noise, not a comparable baseline).

// perfBaselineTask is a single task the two hot-path modes are measured
// against; dispatchedAt is the shared RFC3339 dispatch time both the task and
// the server-issued identity carry.
func perfBaselineTask() (Task, time.Time) {
	dispatchedAt := time.Now().Truncate(time.Second)
	return Task{
		ID:           uuid.NewString(),
		RuntimeID:    uuid.NewString(),
		DispatchedAt: dispatchedAt.Format(time.RFC3339),
	}, dispatchedAt
}

// perfBaselineDaemon builds the Daemon + fakes the opt-in hot path relies on,
// mirroring hotPathFakes but without a *testing.T (a benchmark owns no test).
// The bind/grant/supervisor seams are server-authoritative and the worker is a
// faithful, launchable transport so the extracted path is driven by real, not
// canned, behavior.
func perfBaselineDaemon() (*Daemon, Task) {
	task, dispatchedAt := perfBaselineTask()
	execID := uuid.NewString()
	workerInstanceID := strings.Repeat("a", 32)

	worker := &workerProcessClient{
		transport:  &fakeWorkerTransport{},
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: workerInstanceID,
	}
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:        "https://server.example",
			WorkerProcessEnabled: true,
			NativeHostExecutable: "/test-owned/exec",
		},
		workerProcessLaunch: func(context.Context, string) (*workerProcessClient, error) {
			return worker, nil
		},
		acquireSupervisor: func(context.Context, string, protocol.SupervisorRequest) (*protocol.SupervisorResponse, error) {
			return &protocol.SupervisorResponse{
				InstanceID:   "instance",
				Epoch:        1,
				Capabilities: []string{protocol.ExecutionCapabilityV1},
			}, nil
		},
		bindExecution: func(_ context.Context, rid, tid string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			return &protocol.ExecutionIdentity{
				TaskID:       tid,
				RuntimeID:    rid,
				WorkerID:     req.WorkerID,
				ExecutionID:  execID,
				DispatchedAt: req.DispatchedAt,
			}, nil
		},
		issueGrant: func(_ context.Context, rid, tid string, req protocol.ExecutionGrantRequest) (*protocol.ExecutionGrantResponse, error) {
			return &protocol.ExecutionGrantResponse{
				Token:     "mwt_" + strings.Repeat("a", 64),
				ExpiresAt: time.Now().Add(time.Hour),
				Identity: protocol.ExecutionIdentity{
					TaskID:       tid,
					RuntimeID:    rid,
					WorkerID:     workerInstanceID,
					ExecutionID:  req.ExecutionID,
					DispatchedAt: dispatchedAt,
				},
			}, nil
		},
	}
	return d, task
}

// BenchmarkWorkerHotPathGateOff measures the ORIGINAL in-process hot path: with
// the move-out gate OFF, attemptWorkerRun must return a zero decision without
// launching anything, so the control parent settles in-process. This is the
// baseline the extraction must not regress.
func BenchmarkWorkerHotPathGateOff(b *testing.B) {
	d := &Daemon{cfg: Config{}} // gate OFF by default: the legacy in-process runner.
	task, _ := perfBaselineTask()
	env := &execenv.Environment{WorkDir: "/w", RootDir: "/r"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decision, err := d.attemptWorkerRun(context.Background(), task, "codex", nil, env)
		if err != nil {
			b.Fatalf("gate off: %v", err)
		}
		if decision.UseWorkerResult {
			b.Fatalf("gate off must not take the worker path")
		}
	}
}

// BenchmarkWorkerHotPathOptIn measures the NEW extracted hot path: with the gate
// ON and a launchable bind, attemptWorkerRun drives the worker (bind -> run ->
// stop) through a faithful transport and returns the genuine, un-fabricated
// result the control parent projects. Compared against
// BenchmarkWorkerHotPathGateOff it is the F6 comparable baseline of the
// extraction's per-execution overhead.
func BenchmarkWorkerHotPathOptIn(b *testing.B) {
	d, task := perfBaselineDaemon()
	env := &execenv.Environment{WorkDir: "/w", RootDir: "/r"}

	if !d.workerProcessEnabled() {
		b.Fatalf("precondition: gate must be ON")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decision, err := d.attemptWorkerRun(context.Background(), task, "codex", nil, env)
		if err != nil {
			b.Fatalf("opt-in: %v", err)
		}
		if !decision.UseWorkerResult {
			b.Fatalf("opt-in + launchable bind must take the worker path")
		}
		if !decision.Result.Ran || decision.Result.Status != "completed" {
			b.Fatalf("expected a genuine completed run, got %+v", decision.Result)
		}
	}
}
