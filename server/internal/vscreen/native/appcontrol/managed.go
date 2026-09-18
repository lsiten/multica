package appcontrol

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// ManagedWindow is an owned-window discovery hint, never an input observation.
// It deliberately omits titles, paths, process identifiers and snapshot/element handles.
type ManagedWindow struct {
	Handle   string `json:"window_handle"`
	BundleID string `json:"bundle_id"`
}

// ValidateManagedWindows bounds metadata before crossing the private control channel.
func ValidateManagedWindows(windows []ManagedWindow) error {
	if len(windows) > 128 {
		return refusal("stale_window")
	}
	seen := make(map[string]bool, len(windows))
	for _, w := range windows {
		if w.Handle == "" || len(w.Handle) > 128 || w.BundleID == "" || len(w.BundleID) > 255 || !utf8.ValidString(w.Handle) || !utf8.ValidString(w.BundleID) || strings.ContainsAny(w.Handle+w.BundleID, "\x00\r\n") || seen[w.Handle] {
			return refusal("stale_window")
		}
		seen[w.Handle] = true
	}
	raw, err := json.Marshal(windows)
	if err != nil || len(raw) > 48*1024 {
		return refusal("stale_window")
	}
	return nil
}

// ManagedWindows discovers only this lease's current virtual, non-human owned windows.
// Listing never refreshes AX snapshots, moves a window, or authorizes an action.
func (c *Controller) ManagedWindows(ctx context.Context, a Authority) ([]ManagedWindow, error) {
	ctx, leave, err := c.enter(ctx, a.Resource)
	if err != nil {
		return nil, err
	}
	defer leave()
	d, err := c.authorize(ctx, a, ControlAccess)
	if err != nil {
		return nil, err
	}
	var live struct{ Handles []string }
	if err = c.backend.call(ctx, "managed_windows", d, &live); err != nil {
		return nil, err
	}
	if len(live.Handles) > 128 {
		return nil, refusal("stale_window")
	}
	present := make(map[string]bool, len(live.Handles))
	for _, h := range live.Handles {
		if h == "" || len(h) > 128 || present[h] {
			return nil, refusal("stale_window")
		}
		present[h] = true
	}
	windows := make([]ManagedWindow, 0)
	for handle, owned := range c.windows {
		w := owned.window
		if !present[handle] || owned.human || owned.nativeReleased || owned.display != d || w.DisplayID != d.ID || !d.Bounds.Contains(w.Bounds) {
			continue
		}
		windows = append(windows, ManagedWindow{Handle: handle, BundleID: w.Process.BundleID})
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Handle < windows[j].Handle })
	if err = ValidateManagedWindows(windows); err != nil {
		return nil, err
	}
	if _, err = c.authorize(ctx, a, ControlAccess); err != nil {
		return nil, err
	}
	return windows, nil
}
