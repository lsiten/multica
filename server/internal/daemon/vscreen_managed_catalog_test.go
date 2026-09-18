package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func managedReply(t *testing.T, content []map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	if len(content) == 0 || json.Unmarshal([]byte(content[0]["text"].(string)), &out) != nil {
		t.Fatal("invalid MCP metadata")
	}
	return out
}
func managedInvoke(t *testing.T, e *vscreenExecution, name string, args any) ([]map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return e.invoke(t.Context(), name, raw)
}

func TestManagedWindowTaskReuseRequiresFreshObservation(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	first := newVscreenExecution(t.Context(), Task{ID: "first"}, a, f, nil)
	if _, err := managedInvoke(t, first, "vscreen_acquire", map[string]any{"request_id": "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := managedInvoke(t, first, "vscreen_launch_app", map[string]any{"transaction_id": first.lease.TransactionID, "bundle_id": "fixture"}); err != nil {
		t.Fatal(err)
	}
	oldRevision := f.revision
	if _, err := managedInvoke(t, first, "vscreen_release", map[string]any{"transaction_id": first.lease.TransactionID}); err != nil {
		t.Fatal(err)
	}
	first.Close()
	next := newVscreenExecution(t.Context(), Task{ID: "second"}, a, f, nil)
	defer next.Close()
	tracked := []string{}
	next.windowObserved = func(handle string) { tracked = append(tracked, handle) }
	content, err := managedInvoke(t, next, "vscreen_acquire", map[string]any{"request_id": "second"})
	if err != nil {
		t.Fatal(err)
	}
	windows := managedReply(t, content)["managed_windows"].([]any)
	if len(windows) != 1 || windows[0].(map[string]any)["window_handle"] != "window" || len(tracked) != 0 {
		t.Fatal("directory absent or listing claimed task usage")
	}
	action := protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "entry", Text: "new task"}}
	args := map[string]any{"transaction_id": next.lease.TransactionID, "window_handle": "window", "snapshot_revision": oldRevision, "action_id": "type", "sequence": 1, "action": action}
	if _, err := managedInvoke(t, next, "vscreen_type", args); err == nil {
		t.Fatal("old task observation authorized new task")
	}
	if _, err := managedInvoke(t, next, "vscreen_observe", map[string]any{"transaction_id": next.lease.TransactionID, "window_handle": "window"}); err != nil {
		t.Fatal(err)
	}
	args["snapshot_revision"] = f.revision
	if _, err := managedInvoke(t, next, "vscreen_type", args); err != nil {
		t.Fatal(err)
	}
	if f.value != "new task" || len(tracked) != 1 {
		t.Fatal("freshly selected owned window not used")
	}
}
func TestLaunchAndKnownWindowRemainTrackedWhenPNGObserveFails(t *testing.T) {
	for _, launch := range []bool{true, false} {
		t.Run(map[bool]string{true: "successful-launch", false: "existing-managed-window"}[launch], func(t *testing.T) {
			f, a := newVscreenToolFixture(t)
			e := newVscreenExecution(t.Context(), Task{ID: "task"}, a, f, nil)
			defer e.Close()
			tracked := map[string]bool{}
			e.windowObserved = func(h string) { tracked[h] = true }
			if !launch {
				f.windows = []appcontrol.ManagedWindow{{Handle: "window", BundleID: "fixture"}}
			}
			if _, err := e.acquire(t.Context(), vscreenToolArgs{RequestID: "r"}); err != nil {
				t.Fatal(err)
			}
			f.observeFailure = true
			name := "vscreen_observe"
			args := map[string]any{"transaction_id": e.lease.TransactionID, "window_handle": "window"}
			if launch {
				name = "vscreen_launch_app"
				args = map[string]any{"transaction_id": e.lease.TransactionID, "bundle_id": "fixture"}
			}
			if _, err := managedInvoke(t, e, name, args); err == nil || !tracked["window"] {
				t.Fatal("real ownership was lost with screenshot failure")
			}
			if tracked[""] {
				t.Fatal("empty handle was tracked")
			}
		})
	}
}
func TestWholeDisplayObservationNeverCreatesWindowActionProof(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	f.windows = []appcontrol.ManagedWindow{{Handle: "window", BundleID: "fixture"}}
	e := newVscreenExecution(t.Context(), Task{ID: "task"}, a, f, nil)
	defer e.Close()
	e.windowObserved = func(string) { t.Fatal("display observation marked a window used") }
	if _, err := e.acquire(t.Context(), vscreenToolArgs{RequestID: "r"}); err != nil {
		t.Fatal(err)
	}
	content, err := managedInvoke(t, e, "vscreen_observe", map[string]any{"transaction_id": e.lease.TransactionID})
	if err != nil {
		t.Fatal(err)
	}
	metadata := managedReply(t, content)
	if len(content) != 2 || metadata["window_handle"] != "" || metadata["snapshot_revision"] != float64(0) || metadata["observation_scope"] != "display" {
		t.Fatal("display snapshot fabricated window proof")
	}
	action := protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "entry", Text: "forged"}}
	if _, err := managedInvoke(t, e, "vscreen_type", map[string]any{"transaction_id": e.lease.TransactionID, "window_handle": "window", "snapshot_revision": f.revision, "action_id": "type", "sequence": 1, "action": action}); err == nil {
		t.Fatal("display snapshot authorized window action")
	}
}
func TestUnownedWindowObservationCannotBeTracked(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	e := newVscreenExecution(t.Context(), Task{ID: "task"}, a, f, nil)
	defer e.Close()
	e.windowObserved = func(string) { t.Fatal("unowned handle tracked") }
	if _, err := e.acquire(t.Context(), vscreenToolArgs{RequestID: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := managedInvoke(t, e, "vscreen_observe", map[string]any{"transaction_id": e.lease.TransactionID, "window_handle": "foreign"}); err == nil {
		t.Fatal("foreign window observed")
	}
}

func TestManagedStatusDoesNotExposeDirectoryToWaitingTask(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	f.windows = []appcontrol.ManagedWindow{{Handle: "window", BundleID: "fixture"}}
	owner := newVscreenExecution(t.Context(), Task{ID: "owner"}, a, f, nil)
	defer owner.Close()
	if _, err := owner.acquire(t.Context(), vscreenToolArgs{RequestID: "r"}); err != nil {
		t.Fatal(err)
	}
	other := newVscreenExecution(t.Context(), Task{ID: "other"}, a, f, nil)
	defer other.Close()
	reply, err := managedInvoke(t, other, "vscreen_status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(managedReply(t, reply)["managed_windows"].([]any)) != 0 {
		t.Fatal("waiting task saw another task's directory")
	}
}

type unsuccessfulLaunchFixture struct {
	*vscreenToolFixture
	invalidSuccess bool
}

func (f *unsuccessfulLaunchFixture) LaunchApp(_ context.Context, _ appcontrol.Authority, r appcontrol.LaunchRequest) (appcontrol.Window, error) {
	if f.invalidSuccess {
		return appcontrol.Window{Handle: "untrusted", Process: appcontrol.Process{BundleID: "foreign"}}, nil
	}
	return appcontrol.Window{Handle: "unconfirmed", Process: appcontrol.Process{BundleID: r.BundleID}}, errors.New("uncertain fixture launch")
}
func TestFailedOrInvalidLaunchNeverTracksReturnedHandle(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		f, a := newVscreenToolFixture(t)
		apps := &unsuccessfulLaunchFixture{vscreenToolFixture: f, invalidSuccess: invalid}
		e := newVscreenExecution(t.Context(), Task{ID: "task"}, a, apps, nil)
		e.windowObserved = func(string) { t.Error("failed/invalid launch tracked arbitrary handle") }
		if _, err := e.acquire(t.Context(), vscreenToolArgs{RequestID: "r"}); err != nil {
			t.Fatal(err)
		}
		if _, err := managedInvoke(t, e, "vscreen_launch_app", map[string]any{"transaction_id": e.lease.TransactionID, "bundle_id": "fixture"}); err == nil {
			t.Fatal("bad launch accepted")
		}
		e.Close()
	}
}
