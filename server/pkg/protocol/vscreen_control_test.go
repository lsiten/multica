package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVscreenCommandParserRequiresExplicitBoundCommand(t *testing.T) {
	envelope := VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "daemon-boot", RequestID: "req"}
	command := VscreenCommand{VscreenEnvelope: envelope, CommandID: "command", Kind: VscreenCommandRequestTakeover}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVscreenCommand(raw, envelope); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ from, to string }{{`"request_takeover"`, `""`}, {`"request_takeover"`, `"move_to_physical"`}, {`"rt"`, `"foreign"`}, {`"daemon-boot"`, `"old-boot"`}} {
		if _, err := ParseVscreenCommand([]byte(strings.Replace(string(raw), tc.from, tc.to, 1)), envelope); err == nil {
			t.Fatalf("accepted invalid command %v", tc)
		}
	}
}

func TestVscreenSnapshotRejectsUnknownAndDefaultControl(t *testing.T) {
	p := VscreenStateSnapshot{RuntimeID: "rt", State: VscreenStateReady, NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1, StateRevision: 1, ControlState: VscreenControlIdle, Permissions: VscreenPermissions{ScreenRecording: "granted", Accessibility: "granted"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.ControlState = ""
	if err := p.Validate(); err == nil {
		t.Fatal("missing control state accepted")
	}
	p.ControlState = VscreenControlAgent
	if err := p.Validate(); err == nil {
		t.Fatal("agent control without task accepted")
	}
	if err := (VscreenStateSnapshot{}).Validate(); err == nil {
		t.Fatal("zero snapshot accepted")
	}
}

func TestVscreenInterventionRequiresReturnProofForContinuation(t *testing.T) {
	i := VscreenIntervention{VscreenEnvelope: VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "boot", RequestID: "req"}, InterventionID: "intervention", AgentID: "agent", SourceTaskID: "task", Reason: VscreenBackgroundUnsupported, State: VscreenInterventionAwaitingTakeover, Epoch: VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}}
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.State = VscreenInterventionReadyToContinue
	if err := i.Validate(); err == nil {
		t.Fatal("ready without native return receipt accepted")
	}
	i.ReturnReceiptID = "return"
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.State = VscreenInterventionContinued
	if err := i.Validate(); err == nil {
		t.Fatal("continued without linked task accepted")
	}
	i.ContinuationTaskID = "new-task"
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.HumanSummary = strings.Repeat("人", 700)
	if err := i.Validate(); err == nil {
		t.Fatal("oversized human summary accepted")
	}
}
