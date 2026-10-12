package daemon

import (
	"context"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestApplicationIntentOldAckDoesNotOverrideNewCommandAndSyncedIntentRetires(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://offline.invalid", DaemonID: "fixture"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.accountID = "account"
	input := localApplicationRequest{WorkspaceID: uuid.NewString(), RuntimeID: uuid.NewString(), ApplicationID: uuid.NewString(), Revision: 1, CommandID: uuid.NewString(), Action: "start"}
	observation := protocol.ApplicationObservation{InstanceID: uuid.NewString(), Generation: 1, Revision: 1, ProcessState: "running", HealthState: "healthy"}
	intent := localApplicationIntent{Input: input, InstanceID: observation.InstanceID, Generation: 1, DesiredState: "running", Result: &localApplicationResult{CommandID: input.CommandID, Observation: observation}}
	if err := d.writeLocalIntent(intent); err != nil {
		t.Fatal(err)
	}
	if err := d.queueApplicationObservation(input.RuntimeID, observation); err != nil {
		t.Fatal(err)
	}
	old, _ := d.terminalReports.list()
	if len(old) != 1 {
		t.Fatal("initial observation missing")
	}
	intent.Input.CommandID = uuid.NewString()
	intent.Result.CommandID = intent.Input.CommandID
	if err := d.writeLocalIntent(intent); err != nil {
		t.Fatal(err)
	}
	if err := d.queueApplicationObservation(input.RuntimeID, observation); err != nil {
		t.Fatal(err)
	}
	latest, _ := d.terminalReports.list()
	if len(latest) != 1 || latest[0].report.applicationIntentID != intent.Input.CommandID {
		t.Fatal("same process state suppressed a new local intent")
	}
	if err := d.ackLocalApplicationIntent(t.Context(), old[0].report); err != nil {
		t.Fatal(err)
	}
	current, err := d.readLocalIntent(observation.InstanceID)
	if err != nil || current.Synced || current.Input.CommandID != intent.Input.CommandID {
		t.Fatal("old ACK replaced or retired the newer local intent")
	}
	if err := d.ackLocalApplicationIntent(t.Context(), latest[0].report); err != nil {
		t.Fatal(err)
	}
	current, err = d.readLocalIntent(observation.InstanceID)
	if err != nil || !current.Synced {
		t.Fatalf("accepted state did not retire local authority: %+v %v", current, err)
	}
	if err := d.queueApplicationObservation(input.RuntimeID, observation); err != nil {
		t.Fatal(err)
	}
	queued, _ := d.terminalReports.list()
	if queued[0].report.applicationDesiredState != "" {
		t.Fatal("synced intent kept rewriting server desired state")
	}
}

func TestApplicationIntentLockFencesSeparateOwners(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), DaemonID: "fixture"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	other := New(d.cfg, d.logger)
	instanceID := uuid.NewString()
	release, err := d.lockLocalApplicationIntent(t.Context(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if unlock, err := other.lockLocalApplicationIntent(ctx, instanceID); err == nil {
		unlock()
		release()
		t.Fatal("second owner bypassed command journal lock")
	}
	release()
	unlock, err := other.lockLocalApplicationIntent(t.Context(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestApplicationObservationsCoalesceWithoutDeletingNewStateOnOldAck(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://offline.invalid", DaemonID: "fixture"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := protocol.ApplicationObservation{InstanceID: "instance", Generation: 1, Revision: 1, ProcessState: "running", HealthState: "healthy"}
	if err := d.queueApplicationObservation("runtime", first); err != nil {
		t.Fatal(err)
	}
	old, err := d.terminalReports.list()
	if err != nil || len(old) != 1 {
		t.Fatalf("initial queued observation: %d %v", len(old), err)
	}
	next := first
	next.ProcessState = "stopped"
	if err := d.queueApplicationObservation("runtime", next); err != nil {
		t.Fatal(err)
	}
	if err := d.terminalReports.acknowledge(old[0]); err != nil {
		t.Fatal(err)
	}
	current, err := d.terminalReports.list()
	if err != nil || len(current) != 1 || current[0].report.applicationObservation.ProcessState != "stopped" {
		t.Fatalf("old ACK discarded new local stop: %+v %v", current, err)
	}
	if err := d.terminalReports.acknowledge(current[0]); err != nil {
		t.Fatal(err)
	}
	if remaining, err := d.terminalReports.list(); err != nil || len(remaining) != 0 {
		t.Fatalf("current ACK not applied: %d %v", len(remaining), err)
	}
}

func TestUnchangedApplicationObservationKeepsHeartbeatWithoutRepeatedWrites(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://offline.invalid", DaemonID: "fixture"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	d.reportNow = func() time.Time { return now }
	observation := protocol.ApplicationObservation{InstanceID: "instance", Generation: 1, Revision: 1, ProcessState: "running", HealthState: "healthy"}
	if err := d.queueApplicationObservation("runtime", observation); err != nil {
		t.Fatal(err)
	}
	queued, err := d.terminalReports.list()
	if err != nil || len(queued) != 1 {
		t.Fatal(err)
	}
	if err := d.terminalReports.acknowledge(queued[0]); err != nil {
		t.Fatal(err)
	}
	if err := d.queueApplicationObservation("runtime", observation); err != nil {
		t.Fatal(err)
	}
	if again, err := d.terminalReports.list(); err != nil || len(again) != 0 {
		t.Fatalf("unchanged wake generated another write: %d %v", len(again), err)
	}
	now = now.Add(31 * time.Second)
	if err := d.queueApplicationObservation("runtime", observation); err != nil {
		t.Fatal(err)
	}
	if heartbeat, err := d.terminalReports.list(); err != nil || len(heartbeat) != 1 {
		t.Fatalf("heartbeat suppressed past freshness window: %d %v", len(heartbeat), err)
	}
}
