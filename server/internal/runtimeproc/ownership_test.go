package runtimeproc

import (
	"encoding/json"
	"testing"
)

func TestOwnerReconciliationMatchesLaunchAdmission(t *testing.T) {
	scope := Scope{Backend: "https://example.test", Account: "fixture", Profile: "fixture", DaemonID: "daemon", Service: "fixture"}
	prior, err := NewIdentity(scope, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewIdentity(scope, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, state string
		receipts    map[string]Receipt
		needs       bool
	}{
		{"clean stop", "stopped", nil, false},
		{"completed stop", "stopped", map[string]Receipt{"done": {State: "completed"}}, false},
		{"pending stop", "stopped", map[string]Receipt{"done": {State: "completed"}, "unknown": {State: "pending"}}, true},
		{"unknown receipt state", "stopped", map[string]Receipt{"unknown": {State: "unknown"}}, true},
		{"starting", "starting", nil, true},
		{"ready", "ready", nil, true},
		{"draining", "draining", nil, true},
		{"suspect", "suspect", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := Record{Identity: prior, State: tc.state, Operations: tc.receipts}
			if got := record.NeedsReconciliation(); got != tc.needs {
				t.Errorf("NeedsReconciliation=%v, want %v", got, tc.needs)
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if got := replaceableRecord(raw, next); got == tc.needs {
				t.Errorf("launch replaceable=%v, needs reconciliation=%v", got, tc.needs)
			}
		})
	}
}
