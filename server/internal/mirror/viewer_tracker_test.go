package mirror

import "testing"

func TestViewerTrackerTransitionsAreIdempotent(t *testing.T) {
	tracker := NewViewerTracker()

	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true }) {
		t.Fatal("first active transition was not a change")
	}
	if tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true }) {
		t.Fatal("duplicate active transition was reported")
	}
	if tracker.SetViewerActiveIf("runtime-1", "viewer-2", false, nil) {
		t.Fatal("an unknown viewer departure changed runtime state")
	}
	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", false, nil) {
		t.Fatal("last viewer departure was not a change")
	}
	if tracker.SetViewerActiveIf("runtime-1", "viewer-1", false, nil) {
		t.Fatal("duplicate idle transition was reported")
	}
}

func TestViewerTrackerActivationChecksGuardAtomically(t *testing.T) {
	tracker := NewViewerTracker()

	if tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return false }) {
		t.Fatal("activation was reported when the guard rejected it")
	}
	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true }) {
		t.Fatal("guard-approved activation was not reported")
	}
}

func TestViewerTrackerOnlyNotifiesZeroAndOneTransitions(t *testing.T) {
	tracker := NewViewerTracker()

	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, nil) {
		t.Fatal("first viewer did not transition runtime to active")
	}
	if tracker.SetViewerActiveIf("runtime-1", "viewer-2", true, nil) {
		t.Fatal("second viewer was reported as a zero-to-one transition")
	}
	if tracker.SetViewerActiveIf("runtime-1", "viewer-2", false, nil) {
		t.Fatal("second viewer departure was reported as a one-to-zero transition")
	}
	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", false, nil) {
		t.Fatal("last viewer departure did not transition runtime to idle")
	}
}

func TestViewerTrackerSupportsAggregateLegacyEvents(t *testing.T) {
	tracker := NewViewerTracker()

	if !tracker.SetViewerActiveIf("runtime-1", "", true, nil) {
		t.Fatal("legacy aggregate activation was not a change")
	}
	if tracker.SetViewerActiveIf("runtime-1", "", true, nil) {
		t.Fatal("duplicate legacy aggregate activation was reported")
	}
	if !tracker.SetViewerActiveIf("runtime-1", "", false, nil) {
		t.Fatal("legacy aggregate departure was not a change")
	}
}

func TestViewerTrackerResetReturnsPreviouslyActiveRuntimes(t *testing.T) {
	tracker := NewViewerTracker()
	tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true })
	tracker.SetViewerActiveIf("runtime-2", "viewer-1", true, func() bool { return true })
	tracker.SetViewerActiveIf("runtime-3", "viewer-1", false, nil)

	got := tracker.Reset([]string{"runtime-1", "runtime-3"})

	if len(got) != 1 || got[0] != "runtime-1" {
		t.Fatalf("reset = %v, want [runtime-1]", got)
	}
	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true }) {
		t.Fatal("runtime did not become eligible for a new transition after reset")
	}
}

func TestViewerTrackerResetIfSkipsGuardRejectedRuntimes(t *testing.T) {
	tracker := NewViewerTracker()
	tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true })
	tracker.SetViewerActiveIf("runtime-2", "viewer-1", true, func() bool { return true })

	got := tracker.ResetIf([]string{"runtime-1", "runtime-2"}, func(runtimeID string) bool {
		return runtimeID == "runtime-1"
	})

	if len(got) != 1 || got[0] != "runtime-1" {
		t.Fatalf("reset = %v, want [runtime-1]", got)
	}
	if got := tracker.ResetIf([]string{"runtime-2"}, nil); len(got) != 1 || got[0] != "runtime-2" {
		t.Fatalf("guard-rejected runtime state = %v, want it to remain active", got)
	}
	if !tracker.SetViewerActiveIf("runtime-1", "viewer-1", true, func() bool { return true }) {
		t.Fatal("reset runtime should be eligible for a fresh transition")
	}
}
