package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocalApplicationLifecycleOfflineAndOldCommandReplay(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "offline", 503)
	}))
	defer backend.Close()
	d, command := applicationManagerFixture(t, backend.URL)
	d.cfg.ServerBaseURL = backend.URL
	d.accountID = "verified-fixture-account"
	command.AccessAgentID, command.AccessUserID = command.ApplicationID, d.accountID
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopOwnedApplications)
	input := localApplicationRequest{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, ApplicationID: command.ApplicationID, Revision: command.Revision}
	call := func(action string) localApplicationRequest {
		t.Helper()
		input.Action, input.CommandID = action, uuid.NewString()
		result, err := d.runLocalApplication(ctx, input)
		if err != nil || !result.SyncPending {
			t.Fatalf("%s result=%+v err=%v", action, result, err)
		}
		want := "running"
		if action == "stop" {
			want = "stopped"
		}
		if result.Observation.ProcessState != want {
			t.Fatalf("%s observed=%+v", action, result.Observation)
		}
		return input
	}
	call("stop")
	start := call("start")
	restart := call("restart")
	if _, err := d.runLocalApplication(ctx, restart); err != nil {
		t.Fatal(err)
	}
	if _, err := d.runLocalApplication(ctx, start); err != nil {
		t.Fatal(err)
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || strings.Count(string(starts), "start\n") != 3 {
		t.Fatalf("replayed command relaunched service: %q %v", starts, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("local lifecycle waited for %d remote requests", requests.Load())
	}
	intent, err := d.readLocalIntent(command.InstanceID)
	if err != nil || intent == nil || intent.Input.CommandID != restart.CommandID {
		t.Fatalf("old receipt replaced newest intent: %+v %v", intent, err)
	}
	intent.Result = nil
	if err := d.writeLocalIntent(*intent); err != nil {
		t.Fatal(err)
	}
	if _, err := d.runLocalApplication(ctx, restart); err != nil {
		t.Fatal(err)
	}
	starts, _ = os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if strings.Count(string(starts), "start\n") != 3 {
		t.Fatal("receipt recovery reran the business action")
	}
	pending, err := d.terminalReports.list()
	if err != nil || len(pending) != 1 || pending[0].report.applicationDesiredState != "running" {
		t.Fatalf("durable coalesced state missing: %+v %v", pending, err)
	}
	body := map[string]any{"action": "start", "runtime_id": command.RuntimeID, "revision": command.Revision, "idempotency_key": "scoped-task-command"}
	task := Task{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, AgentID: command.AccessAgentID}
	if _, handled, err := d.taskLocalApplicationControl(ctx, task, command.ApplicationID, body); err != nil || !handled {
		t.Fatalf("verified task local routing failed: %v %v", handled, err)
	}
	task.AgentID = uuid.NewString()
	if _, handled, _ := d.taskLocalApplicationControl(ctx, task, command.ApplicationID, body); handled {
		t.Fatal("different agent acquired cached owner permission")
	}
}

func TestLocalApplicationSplitManagerStartsOfflineWithoutRemoteGrant(t *testing.T) {
	d, child, backend := newApplicationProcessFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	if err := child.close(); err != nil {
		t.Fatal(err)
	}
	backend.stopUploader()
	d.applicationProcessMu.Lock()
	d.applicationProcess = nil
	d.applicationProcessMu.Unlock()
	backend.mu.Lock()
	backend.offline = true
	before := backend.requests
	command := backend.command
	backend.mu.Unlock()
	input := localApplicationRequest{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, ApplicationID: command.ApplicationID, Revision: command.Revision, Action: "start", CommandID: uuid.NewString()}
	result, err := d.runLocalApplication(ctx, input)
	if err != nil || result.Observation.ProcessState != "running" {
		t.Fatalf("offline split start=%+v %v", result, err)
	}
	newChild := d.applicationProcess
	t.Cleanup(func() {
		if err := newChild.close(); err != nil {
			t.Error(err)
		}
	})
	input.Action, input.CommandID = "stop", uuid.NewString()
	result, err = d.runLocalApplication(ctx, input)
	if err != nil || result.Observation.ProcessState != "stopped" {
		t.Fatalf("offline split stop=%+v %v", result, err)
	}
	backend.mu.Lock()
	after := backend.requests
	backend.mu.Unlock()
	if after != before {
		t.Fatalf("local split lifecycle sent %d remote requests", after-before)
	}
	input.Action, input.CommandID = "start", uuid.NewString()
	if _, err = d.runLocalApplication(ctx, input); err != nil {
		t.Fatal(err)
	}
	d.stopUnavailableWorkspaceApplications(ctx, map[string]string{})
	records, err := d.applicationRecords()
	if err != nil || len(records) != 1 || records[0].Observation.ProcessState != "stopped" {
		t.Fatalf("offline local runtime survived membership revocation: %+v %v", records, err)
	}
}

func TestApplicationStartsWhenHostDiagnosticLogCannotOpen(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.invalid")
	directory, err := d.applicationDirectory(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory+string(os.PathSeparator)+"host-start.log", 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopOwnedApplications)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	observation, err := d.executeApplication(ctx, command)
	if err != nil || observation.ProcessState != "running" {
		t.Fatalf("diagnostic log failure prevented service launch: %+v %v", observation, err)
	}
}

func TestLocalApplicationColdRecipeStartsWithoutPriorHostRecord(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://offline.invalid")
	d.accountID = "verified-fixture-account"
	d.applicationCommands.Store(command.InstanceID, command)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	input := localApplicationRequest{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, ApplicationID: command.ApplicationID, Revision: command.Revision, Action: "start", CommandID: uuid.NewString()}
	result, err := d.runLocalApplication(ctx, input)
	if err != nil || result.Observation.ProcessState != "running" {
		t.Fatalf("cold local recipe did not start: %+v %v", result, err)
	}
	t.Cleanup(d.stopOwnedApplications)
}
