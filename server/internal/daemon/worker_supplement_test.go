package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestLostWorkerSupplementHintDoesNotPoisonExecutionAuthority(t *testing.T) {
	transport := &fakeWorkerTransport{failNext: true}
	worker := &workerProcessClient{transport: transport, gate: make(chan struct{}, 1)}
	if err := worker.notifySupplement(t.Context()); err == nil {
		t.Fatal("fixture did not lose the notification")
	}
	status, err := worker.bind(context.Background(), workerRunInput{
		Grant: validWorkerGrant(), CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true,
	})
	if err != nil || !status.Launchable {
		t.Fatalf("content-free hint failure poisoned execution authority: status=%+v err=%v", status, err)
	}
}

func TestWorkerSupplementWakeupSignalsOnlyBoundExecution(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "bound execution", false: "foreign execution"}[matching], func(t *testing.T) {
			s := newWorkerService(t)
			grant := validWorkerGrant()
			if _, err := s.mutate(t.Context(), bindRequest(runtimeproc.Request{}, workerBindRequest{
				Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true,
			})); err != nil {
				t.Fatal(err)
			}
			wakeup, unsubscribe := s.supplements.subscribe(grant.Identity.TaskID)
			defer unsubscribe()
			identity := grant.Identity
			if !matching {
				identity.ExecutionID = validWorkerGrant().Identity.ExecutionID
			}
			payload, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			_, problem := s.mutate(t.Context(), runtimeproc.Request{Operation: "worker.supplement-wakeup", Payload: payload})
			if matching && problem != nil {
				t.Fatalf("bound execution refused: %v", problem)
			}
			if !matching && (problem == nil || problem.Code != "execution_changed") {
				t.Fatalf("foreign execution accepted: %v", problem)
			}
			select {
			case <-wakeup:
				if !matching {
					t.Fatal("foreign execution signalled provider")
				}
			default:
				if matching {
					t.Fatal("bound worker did not receive supplement wakeup")
				}
			}
		})
	}
}
