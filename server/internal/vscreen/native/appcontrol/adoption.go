package appcontrol

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"
)

// WindowCandidate contains only local-owner display metadata and an opaque selection token.
type WindowCandidate struct {
	Handle   string `json:"handle"`
	BundleID string `json:"bundle_id"`
	Title    string `json:"title"`
}

// WindowCandidates must remain on the private local-owner transport, never in model transcripts.
type WindowCandidates struct {
	Windows   []WindowCandidate `json:"windows"`
	Truncated bool              `json:"truncated"`
}

// Validate bounds private candidate metadata and rejects duplicate selection tokens.
func (v WindowCandidates) Validate() error {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 48*1024 || len(v.Windows) > 64 {
		return refusal("stale_window")
	}
	seen := map[string]bool{}
	for _, w := range v.Windows {
		if w.Handle == "" || len(w.Handle) > 128 || w.BundleID == "" || len(w.BundleID) > 255 || len(w.Title) > 256 || !utf8.ValidString(w.Title) || seen[w.Handle] {
			return refusal("stale_window")
		}
		seen[w.Handle] = true
	}
	return nil
}

type nativeCandidate struct {
	Window Window
	Title  string
}
type nativeCandidates struct {
	Windows   []nativeCandidate
	Truncated bool
}
type candidateRecord struct {
	window       Window
	display      Display
	intervention string
	expires      time.Time
}

func (c *Controller) humanSelection(ctx context.Context, r HumanRequest, direction string) (Display, error) {
	if r.Resource.Validate() != nil || r.Grant == "" || r.InterventionID == "" || r.Direction != direction {
		return Display{}, refusal("human_grant_required")
	}
	d, err := c.config.AuthorizeHuman(ctx, r)
	if err != nil {
		return Display{}, err
	}
	if d.Resource != r.Resource || d.Epoch.Validate() != nil || !d.Virtual || d.ID == 0 || !d.Bounds.valid() {
		return Display{}, refusal("human_grant_required")
	}
	if err = c.backend.call(ctx, "quiesce", map[string]any{"Resource": r.Resource}, nil); err != nil {
		return Display{}, err
	}
	c.mu.Lock()
	c.frozen[r.Resource] = true
	c.mu.Unlock()
	return d, nil
}

// ListWindows offers short-lived candidates only after a separate local-owner grant.
func (c *Controller) ListWindows(ctx context.Context, r HumanRequest) (WindowCandidates, error) {
	ctx, leave, err := c.enter(ctx, r.Resource)
	if err != nil {
		return WindowCandidates{}, err
	}
	defer leave()
	d, err := c.humanSelection(ctx, r, "list_existing")
	if err != nil {
		return WindowCandidates{}, err
	}
	if r.WindowHandle != "" {
		return WindowCandidates{}, refusal("human_grant_required")
	}
	now := time.Now()
	if c.candidates == nil {
		c.candidates = make(map[string]candidateRecord)
	}
	for id, v := range c.candidates {
		if !now.Before(v.expires) || v.display.Resource == r.Resource {
			delete(c.candidates, id)
		}
	}
	var native nativeCandidates
	if err = c.backend.call(ctx, "list_windows", nil, &native); err != nil {
		return WindowCandidates{}, err
	}
	if len(native.Windows) > 64 {
		return WindowCandidates{}, refusal("stale_window")
	}
	out := WindowCandidates{Windows: []WindowCandidate{}, Truncated: native.Truncated}
	processCount := make(map[Process]int)
	for _, v := range native.Windows {
		processCount[v.Window.Process]++
	}
	for _, v := range native.Windows {
		w := v.Window
		if processCount[w.Process] != 1 {
			continue
		}
		if w.Handle == "" || len(w.Handle) > 128 || w.WindowID == 0 || !w.Bounds.valid() || w.Process.BundleID == "" || len(w.Process.BundleID) > 255 || len(v.Title) > 256 || !utf8.ValidString(v.Title) {
			return WindowCandidates{}, refusal("stale_window")
		}
		if _, exists := c.candidates[w.Handle]; exists {
			return WindowCandidates{}, refusal("stale_window")
		}
		owned := false
		for _, current := range c.windows {
			if current.window.Process == w.Process {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		claim, e := nativeClaim(w.Process)
		if e != nil {
			continue
		}
		if e = claim.ReleaseAfterQuiescence(); e != nil {
			return WindowCandidates{}, e
		}
		if len(c.candidates) >= 256 {
			out.Truncated = true
			break
		}
		c.candidates[w.Handle] = candidateRecord{window: w, display: d, intervention: r.InterventionID, expires: now.Add(15 * time.Second)}
		out.Windows = append(out.Windows, WindowCandidate{Handle: w.Handle, BundleID: w.Process.BundleID, Title: v.Title})
	}
	if err := out.Validate(); err != nil {
		return WindowCandidates{}, err
	}
	return out, nil
}

// AdoptWindow claims exactly one locally selected candidate without moving or activating it.
func (c *Controller) AdoptWindow(ctx context.Context, r HumanRequest) (Window, error) {
	ctx, leave, err := c.enter(ctx, r.Resource)
	if err != nil {
		return Window{}, err
	}
	defer leave()
	d, err := c.humanSelection(ctx, r, "adopt_existing")
	if err != nil {
		return Window{}, err
	}
	candidate, ok := c.candidates[r.WindowHandle]
	if !ok || candidate.display != d || candidate.intervention != r.InterventionID || !time.Now().Before(candidate.expires) {
		return Window{}, refusal("stale_window")
	}
	delete(c.candidates, r.WindowHandle)
	if len(c.windows) >= 128 {
		return Window{}, refusal("app_limit")
	}
	claim, err := nativeClaim(candidate.window.Process)
	if err != nil {
		return Window{}, refusal("app_claim_conflict")
	}
	owned := &ownedWindow{window: candidate.window, display: d, claim: claim, human: true}
	c.windows[candidate.window.Handle] = owned
	var w Window
	err = c.backend.call(ctx, "adopt_window", map[string]any{"Window": candidate.window, "Display": d}, &w)
	if err != nil {
		return Window{}, err
	}
	// Retain the claim on unexpected replies until explicit quiescent cleanup.
	if w.Handle != candidate.window.Handle || w.Process != candidate.window.Process || w.WindowID != candidate.window.WindowID || w.Bounds != candidate.window.Bounds {
		return Window{}, refusal("stale_window")
	}
	owned.window = w
	return w, nil
}
