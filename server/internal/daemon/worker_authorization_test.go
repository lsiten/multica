package daemon

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func validWorkerLaunchID() WorkerLaunchAuthorization {
	return WorkerLaunchAuthorization{
		TaskID:           "task-1",
		RuntimeID:        "runtime-1",
		WorkerID:         uuid.NewString(),
		ExecutionID:      uuid.NewString(),
		DispatchedAt:     time.Now().UTC(),
		Granted:          true,
		LaunchAuthorized: true,
		Launchable:       true,
	}
}

func TestWorkerLaunchAuthorizationValidateMatrix(t *testing.T) {
	cases := []struct {
		name    string
		a       WorkerLaunchAuthorization
		wantErr bool
	}{
		{"fully authorized launch is valid", validWorkerLaunchID(), false},
		{"missing task id", func() WorkerLaunchAuthorization { a := validWorkerLaunchID(); a.TaskID = ""; return a }(), false && true},
	}
	_ = cases // replaced below with explicit cases
}

func TestWorkerLaunchAuthorizationValidate(t *testing.T) {
	full := validWorkerLaunchID()

	noTask := full
	noTask.TaskID = ""

	noRuntime := full
	noRuntime.RuntimeID = ""

	noWorker := full
	noWorker.WorkerID = ""

	fabricated := full
	fabricated.ExecutionID = "not-a-uuid"

	zeroDispatch := full
	zeroDispatch.DispatchedAt = time.Time{}

	noGrant := full
	noGrant.Granted = false

	noLaunchAuthorized := full
	noLaunchAuthorized.LaunchAuthorized = false

	launchWithReason := full
	launchWithReason.ReasonCode = WorkerAuthReasonNoGrant

	unknownReason := full
	unknownReason.Launchable = false
	unknownReason.Granted = false
	unknownReason.ReasonCode = "made_up_reason"

	contradictory := full
	contradictory.Launchable = false
	contradictory.ReasonCode = WorkerAuthReasonNoLaunchAuth

	properRefuse := full
	properRefuse.Launchable = false
	properRefuse.Granted = false
	properRefuse.ReasonCode = WorkerAuthReasonNoGrant

	uncertainRefuse := full
	uncertainRefuse.Launchable = false
	uncertainRefuse.Granted = false
	uncertainRefuse.LaunchAuthorized = false
	uncertainRefuse.ReasonCode = WorkerAuthReasonUncertain

	cases := []struct {
		name    string
		a       WorkerLaunchAuthorization
		wantErr bool
	}{
		{"fully authorized launch is valid", full, false},
		{"missing task id", noTask, true},
		{"missing runtime id", noRuntime, true},
		{"missing worker id", noWorker, true},
		{"fabricated execution id is rejected", fabricated, true},
		{"zero dispatch is rejected", zeroDispatch, true},
		{"launch without a grant is invalid", noGrant, true},
		{"launch without launch authorization is invalid", noLaunchAuthorized, true},
		{"launch carrying a refusal reason is invalid", launchWithReason, true},
		{"refused launch with an unknown reason is invalid", unknownReason, true},
		{"refused launch holding grant+launch auth is contradictory", contradictory, true},
		{"a refused launch without grant and a known reason is valid", properRefuse, false},
		{"an uncertain ACK refusal is valid (not a launch)", uncertainRefuse, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestDecideWorkerLaunchMatrix(t *testing.T) {
	taskID := "task-1"
	runtimeID := "runtime-1"
	workerID := uuid.NewString()
	identityID := uuid.NewString()
	dispatched := time.Now().UTC()

	cases := []struct {
		name             string
		granted          bool
		launchAuthorized bool
		uncertain        bool
		workerIDMatches  bool
		launchable       bool
		reason           string
	}{
		{"fully authorized admits a launch", true, true, false, true, true, ""},
		{"an uncertain ACK never authorizes a launch", true, true, true, true, false, WorkerAuthReasonUncertain},
		{"a missing grant refuses", false, true, false, true, false, WorkerAuthReasonNoGrant},
		{"a missing launch authorization refuses", true, false, false, true, false, WorkerAuthReasonNoLaunchAuth},
		{"a worker id that does not match the bind refuses", true, true, false, false, false, WorkerAuthReasonWorkerMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := DecideWorkerLaunch(taskID, runtimeID, workerID, identityID, dispatched,
				c.workerIDMatches, c.granted, c.launchAuthorized, c.uncertain)
			if a.Launchable != c.launchable {
				t.Fatalf("Launchable = %v, want %v (reason %q)", a.Launchable, c.launchable, a.ReasonCode)
			}
			if c.reason != "" && a.ReasonCode != c.reason {
				t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, c.reason)
			}
			if err := a.Validate(); err != nil {
				t.Fatalf("decided authorization does not validate: %v", err)
			}
		})
	}
}

func TestDecideWorkerLaunchRejectsFabricatedIdentity(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"fabricated execution id", "made-up-id"},
		{"empty execution id", ""},
		{"execution id with a suffix", uuid.NewString() + "-x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := DecideWorkerLaunch("task-1", "runtime-1", uuid.NewString(), c.id, time.Now().UTC(),
				true, true, true, false)
			if a.Launchable {
				t.Fatalf("launch admitted on fabricated/invalid identity %q", c.id)
			}
			if a.ReasonCode != WorkerAuthReasonNoIdentity {
				t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, WorkerAuthReasonNoIdentity)
			}
		})
	}
	a := DecideWorkerLaunch("task-1", "runtime-1", uuid.NewString(), uuid.NewString(), time.Time{},
		true, true, true, false)
	if a.Launchable || a.ReasonCode != WorkerAuthReasonNoIdentity {
		t.Fatalf("zero dispatch must refuse: %+v", a)
	}
}

func TestDecideWorkerLaunchUncertainNeverAdmits(t *testing.T) {
	a := DecideWorkerLaunch("task-1", "runtime-1", uuid.NewString(), uuid.NewString(), time.Now().UTC(),
		true, true, true, true)
	if a.Launchable {
		t.Fatal("an uncertain ACK admitted a launch")
	}
	if a.ReasonCode != WorkerAuthReasonUncertain {
		t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, WorkerAuthReasonUncertain)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("uncertain refusal must validate: %v", err)
	}
}
