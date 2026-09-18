package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func handleInputQualificationEntrypoint() bool {
	var err error
	switch {
	case smokefixture.IsQualificationExecutable():
		err = smokefixture.RunQualification()
	case len(os.Args) == 2 && os.Args[1] == smokefixture.QualificationHostCommand:
		err = native.RunInputQualificationHost(version + "/" + commit)
	case len(os.Args) == 3 && os.Args[1] == "internal-vscreen-input-qualification":
		err = runVscreenInputQualification(os.Stdout, os.Args[2])
	case len(os.Args) == 4 && os.Args[1] == "internal-vscreen-input-qualification" && os.Args[3] == "--interactive":
		err = runVscreenInputQualificationMode(os.Stdout, os.Args[2], true)
	default:
		return false
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "input_qualification_failed")
		os.Exit(1)
	}
	return true
}

type qualificationStage struct {
	Action             string                `json:"action"`
	Before             qualificationSnapshot `json:"before"`
	After              qualificationSnapshot `json:"after"`
	Mechanism          string                `json:"mechanism"`
	CompletionVerified bool                  `json:"completion_verified"`
	Image              *smokeImage           `json:"image"`
	Revision           uint64                `json:"revision"`
}
type qualificationResult struct {
	Manual                       qualificationManualEvidence `json:"manual"`
	ForegroundSamples            []smokefixture.Foreground   `json:"foreground_samples"`
	Native                       vscreenSmokeResult          `json:"native"`
	Scope                        string                      `json:"scope"`
	ForegroundContinuity         string                      `json:"foreground_continuity"`
	ForegroundSnapshotsUnchanged bool                        `json:"foreground_snapshots_unchanged"`
	ProductionCertified          bool                        `json:"production_certified"`
	EffectsVerified              bool                        `json:"effects_verified"`
	FixtureClosed                bool                        `json:"fixture_closed"`
	ControlRevoked               bool                        `json:"control_revoked"`
	OldLeaseRefused              bool                        `json:"old_lease_refused"`
	CompletionVerified           bool                        `json:"completion_verified"`
	Stages                       []qualificationStage        `json:"stages"`
}

func runVscreenInputQualification(out io.Writer, evidence string) error {
	return runVscreenInputQualificationMode(out, evidence, false)
}
func runVscreenInputQualificationMode(out io.Writer, evidence string, interactive bool) (runErr error) {
	result := qualificationResult{Manual: qualificationManualEvidence{Status: "unverified", Source: "local_app_events_and_explicit_user_confirmation_not_hardware_attestation"}, Scope: "experimental-same-bundle-disposable-fixture", ForegroundContinuity: "unverified_requires_human_typing_phase", Native: vscreenSmokeResult{Scenario: "input-qualification", Version: version, Commit: commit, Status: "blocked"}}
	defer func() {
		if runErr != nil {
			result.Native.Error = qualificationReason(runErr)
		} else {
			result.Native.Status = "passed"
		}
		runErr = errors.Join(runErr, json.NewEncoder(out).Encode(result))
	}()
	if os.Getenv("MULTICA_RUN_VSCREEN_GUI_SMOKE") != "1" {
		return errors.New("gui_not_authorized")
	}
	if !native.Supported() || !filepath.IsAbs(evidence) {
		return errors.New("qualification_unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	result.Native.Executable = executable
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return err
	}
	key := protocol.ResourceKey{BackendIdentity: "https://vscreen-qualification.invalid", WorkspaceID: "qualification", RuntimeID: hex.EncodeToString(id[:]), UID: uint32(os.Getuid())}
	var manual *qualificationManual
	if interactive {
		manual, err = startQualificationManual(ctx, executable, evidence, key, &result.Manual)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, manual.close()) }()
		if err = manual.ready(ctx); err != nil {
			return err
		}
	}
	app, scope, err := smokefixture.PrepareQualification(executable, evidence, key)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := app.Stop(cleanup)
		result.FixtureClosed = e == nil
		runErr = errors.Join(runErr, e)
	}()
	client, err := hostclient.StartInputQualification(ctx, hostclient.Config{Executable: executable, Build: version + "/" + commit, CallTimeout: 4 * time.Second, ShutdownTimeout: 8 * time.Second}, scope)
	if err != nil {
		return err
	}
	result.Native.GUIExercised = true
	// Reuse the existing display lifecycle harness; only its callback selection uses input.
	result.Native.Scenario = "input"
	err = exerciseSmokeDisplay(ctx, client, key, &result.Native, func(_ native.SourceDescriptor) (*smokeVideoResult, error) {
		a := appcontrol.Authority{Resource: key, Epoch: result.Native.Epoch, TaskID: "qualification", TransactionID: "fixture-input", LeaseEpoch: 1}
		return nil, exerciseQualification(ctx, client, app, scope, a, &result, evidence, smokefixture.Snapshot, manual)
	})
	result.Native.Scenario = "input-qualification"
	return err
}
func qualificationElement(o appcontrol.Observation, title string) (appcontrol.Element, error) {
	for _, e := range o.Elements {
		if e.Title == title {
			return e, nil
		}
	}
	return appcontrol.Element{}, errors.New("qualification_element_missing")
}
func qualificationPoint(o appcontrol.Observation, x, y float64) protocol.VscreenPoint {
	return protocol.VscreenPoint{X: (x - o.Window.Bounds.X) * float64(o.Width) / o.Window.Bounds.Width, Y: (y - o.Window.Bounds.Y) * float64(o.Height) / o.Window.Bounds.Height}
}
func waitQualification(ctx context.Context, read func() (smokefixture.QualificationState, error), predicate func(smokefixture.QualificationState) bool) (smokefixture.QualificationState, error) {
	limit, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		s, e := read()
		if e == nil && predicate(s) {
			return s, nil
		}
		select {
		case <-limit.Done():
			return s, errors.New("qualification_effect_unverified")
		case <-ticker.C:
		}
	}
}
func exerciseQualification(ctx context.Context, client smokeInputClient, app smokeOwnedApp, scope smokefixture.QualificationScope, a appcontrol.Authority, result *qualificationResult, evidence string, snapshot func() (smokefixture.Foreground, error), manualPhase ...*qualificationManual) (runErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := client.Revoke(cleanup, a)
		result.ControlRevoked = e == nil
		stop := app.Stop(cleanup)
		result.FixtureClosed = stop == nil
		runErr = errors.Join(runErr, e, stop)
	}()
	var manual *qualificationManual
	if len(manualPhase) > 0 {
		manual = manualPhase[0]
	}
	foreground, err := snapshot()
	if err != nil || foreground.PID <= 0 || manual != nil && foreground.PID != manual.pid {
		return errors.New("foreground_unavailable")
	}
	result.ForegroundSamples = append(result.ForegroundSamples, foreground)
	if err = client.Grant(ctx, a, 15*time.Second); err != nil {
		return err
	}
	if err = client.ResumeApps(ctx, a); err != nil {
		return err
	}
	app.BeforeLaunch()
	window, err := client.LaunchApp(ctx, a, appcontrol.LaunchRequest{BundleID: scope.BundleID})
	if err != nil {
		return err
	}
	valid := func(s smokefixture.QualificationState) bool {
		return s.PID == window.Process.PID && s.ProcessStart == window.Process.Start && s.WindowID == window.WindowID && s.DisplayID == window.DisplayID && !s.Closed
	}
	initial, err := waitQualification(ctx, scope.Read, valid)
	if err != nil {
		return err
	}
	if initial.Text != "" || initial.Toggle || initial.ObjectX != 20 || initial.ObjectY != 30 {
		return errors.New("qualification_not_pristine")
	}
	var sequence uint64
	var lastRequest protocol.VscreenActionRequest
	var lastActionResult appcontrol.Result
	observe := func() (appcontrol.Observation, error) {
		if err := client.Renew(ctx, a, 15*time.Second); err != nil {
			return appcontrol.Observation{}, err
		}
		o, e := client.ObserveApp(ctx, a, window.Handle, true)
		if e != nil {
			return o, e
		}
		if o.Window.Handle != window.Handle || o.Window.Process.PID != window.Process.PID || o.Window.Process.Start != window.Process.Start || o.Window.WindowID != window.WindowID || o.Window.DisplayID != window.DisplayID || o.Display.Resource != a.Resource || o.Display.Epoch != a.Epoch || !o.Display.Virtual || o.PIDInputVerification != "none" || o.Window.SnapshotRevision == 0 {
			return o, errors.New("qualification_observation_changed")
		}
		return o, validateSmokeImageSize(o)
	}
	act := func(o appcontrol.Observation, action protocol.VscreenAction) error {
		sequence++
		lastRequest = smokeAction(a, o, sequence, fmt.Sprintf("qualification-%d", sequence), action)
		r, e := client.ActApp(ctx, lastRequest)
		if e != nil {
			return e
		}
		lastActionResult = r
		if r.Outcome != protocol.VscreenActionVerified || r.Mechanism != "pid" || !r.CompletionVerified {
			return errors.New("qualification_not_pid_dispatch")
		}
		return nil
	}
	stages := []string{"click", "unicode", "key", "scroll", "drag"}
	for index, name := range stages {
		if manual != nil {
			if e := manual.before(ctx, index); e != nil {
				return e
			}
		}
		before, e := scope.Read()
		if e != nil || !valid(before) {
			return errors.New("qualification_fixture_changed")
		}
		o, e := observe()
		if e != nil {
			return e
		}
		var action protocol.VscreenAction
		switch name {
		case "click":
			node, e := qualificationElement(o, "Qualification PID toggle")
			if e != nil || node.Press {
				return errors.New("qualification_semantic_fallback")
			}
			point := qualificationPoint(o, node.Bounds.X+node.Bounds.Width/2, node.Bounds.Y+node.Bounds.Height/2)
			action = protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &point}}
		case "unicode":
			node, e := qualificationElement(o, "Qualification Unicode input")
			if e != nil || node.SetValue || !before.AXFocused || before.InputSource == "" {
				return errors.New("background_ax_focus_unavailable")
			}
			action = protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: node.Handle, Text: "PID 中文 e\u0301Z"}}
		case "key":
			if !before.AXFocused {
				return errors.New("background_ax_focus_unavailable")
			}
			action = protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "Backspace"}}
		case "scroll":
			node, e := qualificationElement(o, "Qualification scroll")
			if e != nil {
				return e
			}
			action = protocol.VscreenAction{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{Position: qualificationPoint(o, node.Bounds.X+100, node.Bounds.Y+50), DeltaY: -120}}
		case "drag":
			node, e := qualificationElement(o, "Qualification draggable object")
			if e != nil {
				return e
			}
			from := qualificationPoint(o, node.Bounds.X+before.ObjectX+20, node.Bounds.Y+node.Bounds.Height-before.ObjectY-20)
			to := qualificationPoint(o, node.Bounds.X+before.ObjectX+120, node.Bounds.Y+node.Bounds.Height-before.ObjectY-20)
			action = protocol.VscreenAction{Kind: protocol.VscreenActionDrag, Drag: &protocol.VscreenDragAction{From: from, To: to, DurationMS: 250}}
		}
		if e = act(o, action); e != nil {
			return e
		}
		after, e := waitQualification(ctx, scope.Read, func(s smokefixture.QualificationState) bool {
			return valid(s) && smokefixture.QualificationEffect(name, before, s)
		})
		if e != nil {
			return e
		}
		fresh, e := waitQualificationPixels(ctx, o, observe)
		if e != nil {
			return e
		}
		if (name == "unicode" || name == "key") && !smokeValue(fresh, after.Text) {
			return errors.New("qualification_ax_readback_missing")
		}
		image, e := writeSmokePNG(evidence, "qualification-"+name+".png", fresh.PNG)
		if e != nil {
			return e
		}
		current, e := snapshot()
		if e != nil || current != foreground {
			return errors.New("foreground_or_cursor_changed")
		}
		result.ForegroundSamples = append(result.ForegroundSamples, current)
		result.Stages = append(result.Stages, qualificationStage{CompletionVerified: lastActionResult.CompletionVerified, Mechanism: lastActionResult.Mechanism, Action: name, Before: qualificationReportSnapshot(before), After: qualificationReportSnapshot(after), Image: image, Revision: fresh.Window.SnapshotRevision})
	}
	if err = client.Revoke(ctx, a); err != nil {
		return err
	}
	_, err = client.ActApp(ctx, lastRequest)
	if err == nil {
		return errors.New("revoked_action_accepted")
	}
	result.OldLeaseRefused = true
	result.EffectsVerified = true
	result.CompletionVerified = true
	result.ForegroundSnapshotsUnchanged = true
	if manual != nil {
		if e := manual.finish(ctx); e != nil {
			return e
		}
		result.ForegroundContinuity = "verified_manual_fixture_challenge"
	}
	return nil
}

func waitQualificationPixels(ctx context.Context, before appcontrol.Observation, observe func() (appcontrol.Observation, error)) (appcontrol.Observation, error) {
	limit, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		fresh, err := observe()
		if err != nil {
			return fresh, err
		}
		if fresh.Window.SnapshotRevision <= before.Window.SnapshotRevision {
			return fresh, errors.New("stale_qualification_observation")
		}
		if !bytes.Equal(fresh.PNG, before.PNG) {
			return fresh, nil
		}
		select {
		case <-limit.Done():
			return fresh, errors.New("qualification_pixels_unchanged")
		case <-ticker.C:
		}
	}
}

// Keep private readback authority out of CLI/Desktop evidence by construction.
type qualificationSnapshot struct {
	PID               int                       `json:"pid"`
	ProcessStart      string                    `json:"process_start"`
	WindowID          uint32                    `json:"window_id"`
	DisplayID         uint32                    `json:"display_id"`
	Bounds            smokefixture.WindowBounds `json:"bounds"`
	Text              string                    `json:"text"`
	Toggle            bool                      `json:"toggle"`
	SelectionLocation uint64                    `json:"selection_location"`
	SelectionLength   uint64                    `json:"selection_length"`
	ScrollOffset      float64                   `json:"scroll_offset"`
	ObjectX           float64                   `json:"object_x"`
	ObjectY           float64                   `json:"object_y"`
	AXFocused         bool                      `json:"ax_focused"`
	InputSource       string                    `json:"input_source"`
	LastEventType     uint32                    `json:"last_event_type"`
}

func qualificationReportSnapshot(s smokefixture.QualificationState) qualificationSnapshot {
	return qualificationSnapshot{PID: s.PID, ProcessStart: s.ProcessStart, WindowID: s.WindowID, DisplayID: s.DisplayID, Bounds: s.Bounds, Text: s.Text, Toggle: s.Toggle, SelectionLocation: s.SelectionLocation, SelectionLength: s.SelectionLength, ScrollOffset: s.ScrollOffset, ObjectX: s.ObjectX, ObjectY: s.ObjectY, AXFocused: s.AXFocused, InputSource: s.InputSource, LastEventType: s.LastEventType}
}

func qualificationReason(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "qualification_cancelled"
	}
	var remote *hostclient.RemoteError
	if errors.As(err, &remote) {
		return remote.Code
	}
	value := err.Error()
	if regexp.MustCompile(`^[a-z_]{1,96}$`).MatchString(value) {
		for _, prefix := range []string{"qualification_", "manual_", "foreground_", "background_ax_", "gui_not_"} {
			if strings.HasPrefix(value, prefix) {
				return value
			}
		}
	}
	return "qualification_blocked"
}
