package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type inputFixture struct {
	a                  appcontrol.Authority
	window             appcontrol.Window
	state              smokefixture.State
	revision           uint64
	frozen             bool
	cleanup            bool
	acts               int
	unsupportedReason  string
	deliverUnsupported bool
	badReadback        bool
	stopFailure        bool
}

func newInputFixture() *inputFixture {
	return &inputFixture{window: appcontrol.Window{Handle: "owned", WindowID: 9, DisplayID: 42, Process: appcontrol.Process{PID: 123, Start: "start", BundleID: "owned.fixture"}}, state: smokefixture.State{PID: 123, ProcessStart: "start", WindowID: 9}, unsupportedReason: "needs_intervention"}
}
func (f *inputFixture) Grant(_ context.Context, a appcontrol.Authority, _ time.Duration) error {
	f.a = a
	return nil
}
func (f *inputFixture) Renew(context.Context, appcontrol.Authority, time.Duration) error { return nil }
func (f *inputFixture) Revoke(context.Context, appcontrol.Authority) error {
	f.frozen = true
	return nil
}
func (f *inputFixture) ResumeApps(context.Context, appcontrol.Authority) error {
	f.frozen = false
	return nil
}
func (f *inputFixture) LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error) {
	return f.window, nil
}
func (f *inputFixture) ObserveApp(_ context.Context, a appcontrol.Authority, _ string, includePNG bool) (appcontrol.Observation, error) {
	f.revision++
	w := f.window
	w.SnapshotRevision = f.revision
	counter := "presses=0"
	if f.state.Presses == 1 && !f.badReadback {
		counter = "presses=1"
	}
	o := appcontrol.Observation{Window: w, Display: appcontrol.Display{Epoch: a.Epoch, ID: 42, Resource: a.Resource, Virtual: true}, Width: 2, Height: 2, Elements: []appcontrol.Element{{Title: "Multica smoke increment", Handle: "button", Press: true}, {Title: "Multica smoke text", Handle: "text", SetValue: true, Value: f.state.Text}, {Title: "counter", Value: counter}}}
	if includePNG {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		if f.state.Text != "" {
			img.Set(0, 0, color.RGBA{R: 255, A: 255})
		}
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			return o, err
		}
		o.PNG = data.Bytes()
	}
	return o, nil
}
func (f *inputFixture) ActApp(_ context.Context, r protocol.VscreenActionRequest) (appcontrol.Result, error) {
	if f.frozen {
		return appcontrol.Result{}, &hostclient.RemoteError{Code: "stale_authority"}
	}
	f.acts++
	switch r.Action.Kind {
	case protocol.VscreenActionClick:
		f.state.Presses++
		return appcontrol.Result{Outcome: protocol.VscreenActionDispatched}, nil
	case protocol.VscreenActionType:
		f.state.Text = r.Action.Type.Text
		return appcontrol.Result{Outcome: protocol.VscreenActionVerified}, nil
	default:
		f.frozen = true
		if f.deliverUnsupported {
			f.state.Keys++
		}
		return appcontrol.Result{}, &hostclient.RemoteError{Code: f.unsupportedReason}
	}
}
func (f *inputFixture) BeforeLaunch()                     {}
func (f *inputFixture) Read() (smokefixture.State, error) { return f.state, nil }
func (f *inputFixture) Stop(context.Context) error {
	f.cleanup = true
	if f.stopFailure {
		return errors.New("fixture_cleanup_unconfirmed")
	}
	f.state.Closed = true
	return nil
}
func TestVscreenInputSmokeFakeSemanticReadbackAndRefusalMatrix(t *testing.T) {
	f := newInputFixture()
	a := appcontrol.Authority{Epoch: smokeTestEpoch, LeaseEpoch: 1}
	result := &smokeInputResult{}
	foreground := smokefixture.Foreground{PID: 7, WindowID: 9, CursorX: 11, CursorY: 13}
	directory := t.TempDir()
	err := exerciseSmokeInput(t.Context(), f, f, "owned.fixture", a, result, directory, func() (smokefixture.Foreground, error) { return foreground, nil })
	if err != nil || !result.AXPressVerified || !result.AXTextVerified || !result.ForegroundUnchanged || result.PerPIDCertified || !result.FixtureClosed || !result.ControlRevoked || len(result.Refusals) != 3 || f.acts != 5 {
		t.Fatalf("result=%+v acts=%d err=%v", result, f.acts, err)
	}
	for _, refusal := range result.Refusals {
		if refusal.Reason != "needs_intervention" || !refusal.OldLeaseRefused || refusal.DeliveredCount != 0 {
			t.Fatal(refusal)
		}
	}
	for _, artifact := range []*smokeImage{result.Before, result.After} {
		info, err := os.Stat(artifact.Artifact)
		if err != nil || info.Size() == 0 || len(artifact.SHA256) != 64 {
			t.Fatal("missing screenshot evidence", err)
		}
	}
	if result.Before.SHA256 == result.After.SHA256 {
		t.Fatal("unchanged pixels passed")
	}
}
func TestVscreenInputSmokeFailureStopsAndNeverClaimsPass(t *testing.T) {
	for _, scenario := range []string{"foreground", "AX readback", "unexpected refusal", "unexpected delivery", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInputFixture()
			if scenario == "AX readback" {
				f.badReadback = true
			}
			if scenario == "unexpected refusal" {
				f.unsupportedReason = "action_uncertain"
			}
			if scenario == "unexpected delivery" {
				f.deliverUnsupported = true
			}
			if scenario == "cleanup" {
				f.stopFailure = true
			}
			calls := 0
			snapshot := func() (smokefixture.Foreground, error) {
				calls++
				p := 7
				if scenario == "foreground" && calls > 1 {
					p = 8
				}
				return smokefixture.Foreground{PID: p, WindowID: 9}, nil
			}
			result := &smokeInputResult{}
			err := exerciseSmokeInput(t.Context(), f, f, "owned.fixture", appcontrol.Authority{Epoch: smokeTestEpoch, LeaseEpoch: 1}, result, t.TempDir(), snapshot)
			if err == nil || !f.cleanup {
				t.Fatalf("scenario=%s result=%+v error=%v", scenario, result, err)
			}
			if scenario == "foreground" && f.acts != 0 {
				t.Fatal("input continued after foreground changed")
			}
			if (scenario == "unexpected refusal" || scenario == "unexpected delivery") && f.acts != 3 {
				t.Fatal("continued matrix after first unsafe result")
			}
			if scenario == "unexpected refusal" && (len(result.Refusals) != 1 || result.Refusals[0].Reason != "action_uncertain" || !result.Refusals[0].CountersObserved) {
				t.Fatal("actual refusal evidence lost")
			}
			if scenario == "cleanup" && result.FixtureClosed {
				t.Fatal("failed cleanup claimed success")
			}
		})
	}
}
func TestVscreenInputScreenshotRejectsInvalidOrOverwrittenArtifact(t *testing.T) {
	if _, err := writeSmokePNG(t.TempDir(), "image.png", []byte("not PNG")); err == nil {
		t.Fatal("invalid image accepted")
	}
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	directory := t.TempDir()
	if _, err := writeSmokePNG(directory, "image.png", data.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSmokePNG(directory, "image.png", data.Bytes()); err == nil {
		t.Fatal("prior evidence overwritten")
	}
}

func TestVscreenInputCleanupForegroundIsPartOfAcceptance(t *testing.T) {
	baseline := smokefixture.Foreground{PID: 7, WindowID: 9}
	result := &smokeInputResult{ForegroundUnchanged: true, Stages: []smokeInputStage{{Stage: "before-display", Foreground: baseline}}}
	if err := recordSmokeCleanupForeground(result, func() (smokefixture.Foreground, error) { return smokefixture.Foreground{PID: 8, WindowID: 10}, nil }); err == nil || result.ForegroundUnchanged {
		t.Fatal("cleanup changed foreground but acceptance passed")
	}
}
