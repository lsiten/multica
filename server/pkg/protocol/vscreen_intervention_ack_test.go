package protocol

import "testing"

func TestVscreenInterventionAckRejectsAmbiguousResult(t *testing.T) {
	ack := VscreenInterventionAck{VscreenEnvelope: VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "generation", RequestID: "request"}, InterventionID: "intervention", Accepted: true, Version: 7}
	if err := ack.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*VscreenInterventionAck)
	}{
		{"missing_version", func(a *VscreenInterventionAck) { a.Version = 0 }},
		{"success_and_failure", func(a *VscreenInterventionAck) { a.Reason = "stale_generation" }},
		{"raw_error", func(a *VscreenInterventionAck) {
			a.Accepted = false
			a.Version = 0
			a.Reason = "database secret detail"
		}},
		{"failure_version", func(a *VscreenInterventionAck) { a.Accepted = false; a.Reason = "stale_generation" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := ack
			test.mutate(&changed)
			if changed.Validate() == nil {
				t.Fatal("ambiguous ack accepted")
			}
		})
	}
	ack.Accepted = false
	ack.Version = 0
	ack.Reason = "daemon_timeout"
	if err := ack.Validate(); err != nil {
		t.Fatal(err)
	}
}
