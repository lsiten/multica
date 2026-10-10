//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWorkerProcessRuntimeHonorsTerminalCancel is the F3 runtime acceptance for
// "cancel preserved / old cancel": when control reports the execution terminal
// (cancelled/failed/completed), the worker must stop itself and drop its
// client so it can never report or relaunch a result it did not run.
func TestWorkerProcessRuntimeHonorsTerminalCancel(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The worker's Status query asks for the execution's authoritative state.
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"cancelled"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer callback.Close()

	call := spawnWorkerChild(t, callback.URL)
	grant := validWorkerGrant()
	bindStatus := workerBindStatus(t, call, map[string]any{
		"grant":                  grant,
		"callback_url":           callback.URL,
		"worker_id_matches_bind": true,
		"launch_authorized":      true,
		"uncertain":              false,
	})
	if !bindStatus.Launchable {
		t.Fatalf("bind not launchable: reason=%s", bindStatus.ReasonCode)
	}

	// Honor the terminal state: the worker must cancel itself.
	resp, err := call("worker.honor-cancel", map[string]any{})
	if err != nil {
		t.Fatalf("honor-cancel transport failed: %v", err)
	}
	if resp.Receipt == nil || resp.Receipt.Error != nil {
		t.Fatalf("honor-cancel failed: code=%s msg=%s", resp.Receipt.Error.Code, resp.Receipt.Error.Message)
	}
	var cancel workerCancelState
	if err := json.Unmarshal(resp.Receipt.Result, &cancel); err != nil {
		t.Fatalf("unmarshal cancel state: %v (%s)", err, resp.Receipt.Result)
	}
	if !cancel.Cancelled || cancel.ReasonCode != "cancelled" {
		t.Fatalf("worker did not honor the terminal cancel: %+v", cancel)
	}
	t.Logf("OBSERVE worker honored terminal cancel: cancelled=%v task_state=%s", cancel.Cancelled, cancel.ReasonCode)

	// After cancelling, the worker has no client and must not report a result.
	report, err := call("worker.report", map[string]any{"operation": "complete", "payload": map[string]any{}})
	if err != nil {
		t.Fatalf("report transport failed: %v", err)
	}
	if report.Receipt == nil || report.Receipt.Error == nil {
		t.Fatalf("cancelled worker still accepted a report; it should have no client")
	}
	if report.Receipt.Error.Code != "no_client" {
		t.Fatalf("expected no_client after cancel, got code=%s", report.Receipt.Error.Code)
	}
	t.Logf("OBSERVE cancelled worker refused a report: code=%s", report.Receipt.Error.Code)
}
