package protocol

import "testing"

func TestAgentDesktopReadinessContract(t *testing.T) {
	envelope := RuntimeEnvelope{
		WorkspaceID:      "workspace",
		RuntimeID:        "runtime",
		DaemonGeneration: "generation",
		RequestID:        "request",
	}
	ready := AgentDesktopReadiness{
		Platform: "macos",
		Version:  "1.3.1",
		Permissions: AgentDesktopPermissions{
			Accessibility:   AgentDesktopPermissionGranted,
			ScreenRecording: AgentDesktopPermissionGranted,
			Automation:      AgentDesktopPermissionUnknown,
		},
		SurfaceCount: 9,
		Ready:        true,
	}
	if err := (AgentDesktopReadinessResult{RuntimeEnvelope: envelope, Readiness: ready}).Validate(); err != nil {
		t.Fatal(err)
	}
	failed := ready
	failed.Ready = false
	failed.Reason = AgentDesktopReasonAccessibilityDenied
	if err := (AgentDesktopPermissionGuideResult{RuntimeEnvelope: envelope, Readiness: failed}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentDesktopReadinessRejectsContradictoryState(t *testing.T) {
	envelope := RuntimeEnvelope{
		WorkspaceID:      "workspace",
		RuntimeID:        "runtime",
		DaemonGeneration: "generation",
		RequestID:        "request",
	}
	readiness := AgentDesktopReadiness{
		Platform: "macos",
		Version:  "1.3.1",
		Permissions: AgentDesktopPermissions{
			Accessibility:   AgentDesktopPermissionGranted,
			ScreenRecording: AgentDesktopPermissionGranted,
			Automation:      AgentDesktopPermissionUnknown,
		},
		SurfaceCount: 1,
		Ready:        true,
		Reason:       AgentDesktopReasonStatusFailed,
	}
	if err := (AgentDesktopReadinessResult{RuntimeEnvelope: envelope, Readiness: readiness}).Validate(); err == nil {
		t.Fatal("accepted ready state with a failure reason")
	}
}
