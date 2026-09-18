package main

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"testing"
	"time"
)

func TestQualificationManualRequiresActualChallengeDuringActionsAndConfirmation(t *testing.T) {
	for _, failure := range []string{"", "no_confirm", "wrong_text", "no_keys", "before_only", "after_only"} {
		t.Run(failure, func(t *testing.T) {
			result := &qualificationManualEvidence{Status: "unverified", FirstActionNS: 100, LastActionNS: 200}
			state := smokefixture.QualificationState{ManualStarted: true, ManualConfirmed: true, KeyCount: 6, FirstKeyNS: 90, LastKeyNS: 210}
			state.Text = "ABCDEZ"
			switch failure {
			case "no_confirm":
				state.ManualConfirmed = false
			case "wrong_text":
				state.Text = "other"
			case "no_keys":
				state.KeyCount = 0
			case "before_only":
				state.LastKeyNS = 99
			case "after_only":
				state.FirstKeyNS = 201
			}
			m := qualificationManual{result: result}
			err := m.accept(state)
			if (err == nil) != (failure == "") {
				t.Fatalf("%s: %v", failure, err)
			}
			if failure != "" && result.Status != "unverified" {
				t.Fatal("unverified human phase marked passed")
			}
		})
	}
}
func TestQualificationManualPromptNeverSuppliesTextAndTimeoutIsUnverified(t *testing.T) {
	result := &qualificationManualEvidence{Status: "unverified"}
	var prompts []int
	m := qualificationManual{pid: 123, result: result, prompt: func(stage int) error { prompts = append(prompts, stage); return nil }, read: func() (smokefixture.QualificationState, error) {
		return smokefixture.QualificationState{State: smokefixture.State{PID: 123, ProcessStart: "start", WindowID: 9}, ManualStarted: true}, nil
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if err := m.before(ctx, 0); err == nil {
		t.Fatal("missing user key accepted")
	}
	if len(prompts) != 1 || prompts[0] != 1 || result.Status != "unverified" {
		t.Fatal("manual stage fabricated")
	}
}
