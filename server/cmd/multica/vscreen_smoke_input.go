package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const smokeUnicode = "Multica smoke 中文 e\u0301"

type smokeInputClient interface {
	Grant(context.Context, appcontrol.Authority, time.Duration) error
	Renew(context.Context, appcontrol.Authority, time.Duration) error
	Revoke(context.Context, appcontrol.Authority) error
	ResumeApps(context.Context, appcontrol.Authority) error
	LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error)
	ObserveApp(context.Context, appcontrol.Authority, string, bool) (appcontrol.Observation, error)
	ActApp(context.Context, protocol.VscreenActionRequest) (appcontrol.Result, error)
}
type smokeOwnedApp interface {
	BeforeLaunch()
	Read() (smokefixture.State, error)
	Stop(context.Context) error
}
type smokeImage struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}
type smokeInputStage struct {
	CountersObserved bool                    `json:"counters_observed"`
	Stage            string                  `json:"stage"`
	Foreground       smokefixture.Foreground `json:"foreground"`
	Presses          uint64                  `json:"presses"`
	Text             string                  `json:"text"`
	Keys             uint64                  `json:"keys"`
	Scrolls          uint64                  `json:"scrolls"`
	Drags            uint64                  `json:"drags"`
	SnapshotRevision uint64                  `json:"snapshot_revision,omitempty"`
}
type smokeRefusal struct {
	CountersObserved bool   `json:"counters_observed"`
	Action           string `json:"action"`
	Reason           string `json:"reason"`
	OldLeaseRefused  bool   `json:"old_lease_refused"`
	DeliveredCount   uint64 `json:"delivered_count"`
}
type smokeInputResult struct {
	Scope               string            `json:"scope"`
	FixtureBundleID     string            `json:"fixture_bundle_id"`
	FixtureBinarySHA256 string            `json:"fixture_binary_sha256"`
	FixtureWindowID     uint32            `json:"fixture_window_id"`
	FixturePID          int               `json:"fixture_pid"`
	FixtureClosed       bool              `json:"fixture_closed"`
	ControlRevoked      bool              `json:"control_revoked"`
	AXPressVerified     bool              `json:"ax_press_verified"`
	AXTextVerified      bool              `json:"ax_text_verified"`
	ForegroundUnchanged bool              `json:"foreground_snapshots_unchanged"`
	PerPIDCertified     bool              `json:"per_pid_certified"`
	Before              *smokeImage       `json:"before,omitempty"`
	After               *smokeImage       `json:"after,omitempty"`
	Stages              []smokeInputStage `json:"stages"`
	Refusals            []smokeRefusal    `json:"unsupported_actions"`
}

func runSmokeInput(ctx context.Context, client *hostclient.Client, key protocol.ResourceKey, result *vscreenSmokeResult, evidence string) error {
	app, err := smokefixture.Prepare(result.Executable, evidence)
	if err != nil {
		return err
	}
	if result.Input == nil {
		result.Input = &smokeInputResult{Scope: "test-owned-fixture-external-ax-only"}
	}
	result.Input.FixtureBundleID = app.BundleID
	result.Input.FixtureBinarySHA256 = app.BinarySHA256
	authority := appcontrol.Authority{Resource: key, Epoch: result.Epoch, TaskID: "smoke-input", TransactionID: "smoke-input-1", LeaseEpoch: 1}
	return exerciseSmokeInput(ctx, client, app, app.BundleID, authority, result.Input, evidence, smokefixture.Snapshot)
}
func exerciseSmokeInput(ctx context.Context, client smokeInputClient, app smokeOwnedApp, bundleID string, a appcontrol.Authority, result *smokeInputResult, evidence string, snapshot func() (smokefixture.Foreground, error)) (runErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		revokeErr := client.Revoke(cleanup, a)
		result.ControlRevoked = revokeErr == nil
		// Only the fixture self-terminates; native display teardown happens afterwards.
		stopErr := app.Stop(cleanup)
		result.FixtureClosed = stopErr == nil
		runErr = errors.Join(runErr, revokeErr, stopErr)
	}()
	foreground, err := snapshot()
	if err != nil {
		return err
	}
	if foreground.PID <= 0 || foreground.WindowID == 0 {
		return errors.New("foreground_window_unavailable")
	}
	if len(result.Stages) > 0 && result.Stages[0].Foreground != foreground {
		return errors.New("foreground_or_cursor_changed_during_display_creation")
	}
	result.Stages = append(result.Stages, smokeInputStage{Stage: "before-launch", Foreground: foreground})
	if err = client.Grant(ctx, a, 15*time.Second); err != nil {
		return err
	}
	if err = client.ResumeApps(ctx, a); err != nil {
		return err
	}
	app.BeforeLaunch()
	window, err := client.LaunchApp(ctx, a, appcontrol.LaunchRequest{BundleID: bundleID})
	if err != nil {
		return err
	}
	if window.Process.BundleID != bundleID || window.Handle == "" || window.Process.PID <= 0 || window.WindowID == 0 || window.DisplayID == 0 {
		return errors.New("foreign_fixture_window")
	}
	result.FixturePID = window.Process.PID
	result.FixtureWindowID = window.WindowID
	state, err := awaitFixtureState(ctx, app, func(s smokefixture.State) bool {
		return s.PID == window.Process.PID && s.ProcessStart == window.Process.Start && s.WindowID == window.WindowID && !s.Closed
	})
	if err != nil {
		return err
	}
	if state.Presses != 0 || state.Text != "" || state.Keys != 0 || state.Scrolls != 0 || state.Drags != 0 {
		return errors.New("fixture_not_pristine")
	}
	record := func(stage string, revision uint64) error {
		current, e := snapshot()
		if e != nil {
			return e
		}
		value, e := app.Read()
		if e != nil {
			return e
		}
		result.Stages = append(result.Stages, smokeInputStage{CountersObserved: true, Stage: stage, Foreground: current, Presses: value.Presses, Text: value.Text, Keys: value.Keys, Scrolls: value.Scrolls, Drags: value.Drags, SnapshotRevision: revision})
		if current != foreground {
			return errors.New("foreground_or_cursor_changed")
		}
		return nil
	}
	if err = record("launched", 0); err != nil {
		return err
	}
	observe := func(png bool) (appcontrol.Observation, error) {
		if err := client.Renew(ctx, a, 15*time.Second); err != nil {
			return appcontrol.Observation{}, err
		}
		observation, err := client.ObserveApp(ctx, a, window.Handle, png)
		if err == nil && (observation.Window.Process != window.Process || observation.Window.Handle != window.Handle || observation.Window.WindowID != window.WindowID || observation.Display.ID != window.DisplayID || !observation.Display.Virtual || observation.Display.Resource != a.Resource || observation.Display.Epoch != a.Epoch || observation.Window.SnapshotRevision == 0) {
			err = errors.New("fixture_observation_identity_changed")
		}
		return observation, err
	}
	initial, err := observe(true)
	if err != nil {
		return err
	}
	if err = validateSmokeImageSize(initial); err != nil {
		return err
	}
	result.Before, err = writeSmokePNG(evidence, "input-before.png", initial.PNG)
	if err != nil {
		return err
	}
	button, err := smokeElement(initial, "Multica smoke increment", true)
	if err != nil {
		return err
	}
	press := smokeAction(a, initial, 1, "press", protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{ElementHandle: button.Handle}})
	outcome, err := client.ActApp(ctx, press)
	if err != nil {
		return err
	}
	if outcome.Outcome != protocol.VscreenActionDispatched && outcome.Outcome != protocol.VscreenActionVerified {
		return errors.New("press_delivery_uncertain")
	}
	state, err = awaitFixtureState(ctx, app, func(s smokefixture.State) bool { return s.Presses == 1 })
	if err != nil {
		return err
	}
	afterPress, err := observe(false)
	if err != nil {
		return err
	}
	if afterPress.Window.SnapshotRevision <= initial.Window.SnapshotRevision || !smokeValue(afterPress, "presses=1") {
		return errors.New("press_readback_missing")
	}
	result.AXPressVerified = true
	if err = record("pressed", afterPress.Window.SnapshotRevision); err != nil {
		return err
	}
	text, err := smokeElement(afterPress, "Multica smoke text", false)
	if err != nil {
		return err
	}
	typed := smokeAction(a, afterPress, 2, "type", protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: text.Handle, Text: smokeUnicode}})
	outcome, err = client.ActApp(ctx, typed)
	if err != nil {
		return err
	}
	if outcome.Outcome != protocol.VscreenActionVerified {
		return errors.New("text_not_verified")
	}
	state, err = awaitFixtureState(ctx, app, func(s smokefixture.State) bool { return s.Text == smokeUnicode && s.Presses == 1 })
	if err != nil {
		return err
	}
	afterText, err := observe(true)
	if err != nil {
		return err
	}
	if afterText.Window.SnapshotRevision <= afterPress.Window.SnapshotRevision || !smokeValue(afterText, smokeUnicode) {
		return errors.New("text_readback_missing")
	}
	if err = validateSmokeImageSize(afterText); err != nil {
		return err
	}
	result.After, err = writeSmokePNG(evidence, "input-after.png", afterText.PNG)
	if err != nil {
		return err
	}
	if result.After.SHA256 == result.Before.SHA256 {
		return errors.New("fixture_pixels_unchanged")
	}
	result.AXTextVerified = true
	if err = record("typed", afterText.Window.SnapshotRevision); err != nil {
		return err
	}
	actions := []protocol.VscreenAction{
		{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "ArrowLeft"}},
		{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{Position: protocol.VscreenPoint{X: 1, Y: 1}, DeltaY: 1}},
		{Kind: protocol.VscreenActionDrag, Drag: &protocol.VscreenDragAction{From: protocol.VscreenPoint{X: 1, Y: 1}, To: protocol.VscreenPoint{X: 2, Y: 2}, DurationMS: 1}},
	}
	for index, action := range actions {
		// Each negative case starts a new, explicitly granted transaction after a native barrier.
		if err = client.Revoke(ctx, a); err != nil {
			return err
		}
		a.LeaseEpoch++
		a.TransactionID = fmt.Sprintf("smoke-denial-%d", index+1)
		if err = client.Grant(ctx, a, 15*time.Second); err != nil {
			return err
		}
		if err = client.ResumeApps(ctx, a); err != nil {
			return err
		}
		observation, e := observe(false)
		if e != nil {
			return e
		}
		rejected := smokeAction(a, observation, 1, "unsupported-"+string(action.Kind), action)
		_, e = client.ActApp(ctx, rejected)
		var remote *hostclient.RemoteError
		refusal := smokeRefusal{Action: string(action.Kind), Reason: "unexpected_success"}
		if errors.As(e, &remote) {
			refusal.Reason = remote.Code
		} else if errors.Is(e, context.Canceled) {
			refusal.Reason = "cancelled"
		} else if errors.Is(e, context.DeadlineExceeded) {
			refusal.Reason = "deadline_exceeded"
		} else if e != nil {
			refusal.Reason = "operation_failed"
		}
		current, readErr := app.Read()
		if readErr == nil {
			refusal.CountersObserved = true
			refusal.DeliveredCount = current.Keys + current.Scrolls + current.Drags
		}
		result.Refusals = append(result.Refusals, refusal)
		if remote == nil || remote.Code != "needs_intervention" {
			return fmt.Errorf("unsupported_action_not_safely_refused: %s", refusal.Reason)
		}
		if readErr != nil {
			return readErr
		}
		_, oldErr := client.ActApp(ctx, smokeAction(a, observation, 2, "old-lease-input", protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{ElementHandle: button.Handle}}))
		var oldRemote *hostclient.RemoteError
		refused := errors.As(oldErr, &oldRemote) && oldRemote.Code == "stale_authority"
		current, e = app.Read()
		if e != nil {
			return e
		}
		delivered := current.Keys + current.Scrolls + current.Drags
		result.Refusals[len(result.Refusals)-1] = smokeRefusal{CountersObserved: true, Action: string(action.Kind), Reason: remote.Code, OldLeaseRefused: refused, DeliveredCount: delivered}
		if !refused || delivered != 0 || current.Presses != state.Presses || current.Text != state.Text {
			return errors.New("unsupported_action_changed_fixture")
		}
		if err = record("refused-"+string(action.Kind), observation.Window.SnapshotRevision); err != nil {
			return err
		}
	}
	result.ForegroundUnchanged = true
	return nil
}
func awaitFixtureState(ctx context.Context, app smokeOwnedApp, ready func(smokefixture.State) bool) (smokefixture.State, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := app.Read()
		if err == nil && ready(state) {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return state, errors.New("fixture_readback_timeout")
		case <-ticker.C:
		}
	}
}
func smokeAction(a appcontrol.Authority, o appcontrol.Observation, sequence uint64, id string, action protocol.VscreenAction) protocol.VscreenActionRequest {
	return protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: a.Resource, Epoch: a.Epoch, TaskID: a.TaskID, TransactionID: a.TransactionID, LeaseEpoch: a.LeaseEpoch, WindowHandle: o.Window.Handle, SnapshotRevision: o.Window.SnapshotRevision}, Sequence: sequence, ActionID: id, Action: action}
}
func smokeElement(o appcontrol.Observation, title string, press bool) (appcontrol.Element, error) {
	var found *appcontrol.Element
	for _, e := range o.Elements {
		if e.Title == title && (press && e.Press || !press && e.SetValue) {
			if found != nil {
				return appcontrol.Element{}, errors.New("ambiguous_fixture_element")
			}
			copyElement := e
			found = &copyElement
		}
	}
	if found == nil {
		return appcontrol.Element{}, errors.New("fixture_element_missing")
	}
	return *found, nil
}
func smokeValue(o appcontrol.Observation, value string) bool {
	for _, e := range o.Elements {
		if e.Value == value {
			return true
		}
	}
	return false
}
func writeSmokePNG(directory, name string, data []byte) (*smokeImage, error) {
	if len(data) == 0 || len(data) > 8*1024*1024 {
		return nil, errors.New("invalid_fixture_png")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > 16*1024*1024 {
		return nil, errors.New("invalid_fixture_png")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("invalid_fixture_png")
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	digest := sha256.Sum256(data)
	return &smokeImage{Artifact: path, SHA256: hex.EncodeToString(digest[:]), Width: config.Width, Height: config.Height}, nil
}

func recordSmokeCleanupForeground(result *smokeInputResult, snapshot func() (smokefixture.Foreground, error)) error {
	current, err := snapshot()
	if err != nil {
		result.ForegroundUnchanged = false
		return err
	}
	result.Stages = append(result.Stages, smokeInputStage{Stage: "after-cleanup", Foreground: current})
	if len(result.Stages) < 2 || current != result.Stages[0].Foreground {
		result.ForegroundUnchanged = false
		return errors.New("foreground_or_cursor_changed_during_cleanup")
	}
	return nil
}

func validateSmokeImageSize(o appcontrol.Observation) error {
	config, err := png.DecodeConfig(bytes.NewReader(o.PNG))
	if err != nil || uint32(config.Width) != o.Width || uint32(config.Height) != o.Height {
		return errors.New("fixture_screenshot_geometry_mismatch")
	}
	return nil
}
