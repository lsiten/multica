package appcontrol

import (
	"context"
	"encoding/json"
	"testing"
)

func TestManagedWindowsFiltersOwnershipWithoutGrantingObservation(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	original := c.windows["owned"]
	add := func(handle string, change func(*ownedWindow)) {
		v := *original
		v.window.Handle = handle
		change(&v)
		c.windows[handle] = &v
	}
	add("human", func(w *ownedWindow) { w.human = true })
	add("released", func(w *ownedWindow) { w.nativeReleased = true })
	add("foreign", func(w *ownedWindow) { w.display.Resource.RuntimeID = "other" })
	add("closed", func(*ownedWindow) {})
	b.run = func(_ context.Context, op string, _ any, out any) error {
		if op != "managed_windows" {
			t.Fatalf("listing called %s", op)
		}
		raw, _ := json.Marshal(map[string]any{"Handles": []string{"owned", "human", "released", "foreign"}})
		return json.Unmarshal(raw, out)
	}
	windows, err := c.ManagedWindows(t.Context(), a)
	if err != nil || len(windows) != 1 || windows[0].Handle != "owned" || windows[0].BundleID != "fixture" {
		t.Fatalf("windows=%+v err=%v", windows, err)
	}
	if original.window.SnapshotRevision != 1 || len(c.actions) != 0 {
		t.Fatal("listing modified input proof")
	}
	raw, _ := json.Marshal(windows)
	var metadata []map[string]any
	json.Unmarshal(raw, &metadata)
	if len(metadata[0]) != 2 {
		t.Fatal("native identity or titles exposed")
	}
	for _, mutate := range []func(*Authority){func(a *Authority) { a.Resource.RuntimeID = "foreign" }, func(a *Authority) { a.Epoch.GeometryRevision++ }, func(a *Authority) { a.LeaseEpoch++ }, func(a *Authority) { a.TaskID = ""; a.ObserverGrant = "observer" }} {
		bad := a
		mutate(&bad)
		if _, err := c.ManagedWindows(t.Context(), bad); err == nil {
			t.Fatal("foreign/old/observer authority listed managed windows")
		}
	}
}
func TestResumeInvalidatesOldWindowSnapshotBeforeNextTask(t *testing.T) {
	c, b, a, action := controlFixture(t)
	c.frozen[a.Resource] = true
	b.run = func(_ context.Context, op string, _ any, _ any) error {
		if op == "action" {
			t.Fatal("old snapshot reached native input")
		}
		return nil
	}
	if err := c.Resume(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if c.windows["owned"].window.SnapshotRevision != 0 {
		t.Fatal("old proof survived resume")
	}
	if _, err := c.Act(t.Context(), action); err == nil {
		t.Fatal("action did not require fresh observation")
	}
}
