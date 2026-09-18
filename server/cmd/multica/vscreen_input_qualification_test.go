package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type qualificationFake struct {
	*inputFixture
	scope    smokefixture.QualificationScope
	readback smokefixture.QualificationState
	failure  string
}

func (f *qualificationFake) publish() error {
	raw, _ := json.Marshal(f.readback)
	return os.WriteFile(filepath.Join(f.scope.Directory, "readback.json"), raw, 0600)
}
func (f *qualificationFake) ObserveApp(_ context.Context, a appcontrol.Authority, _ string, _ bool) (appcontrol.Observation, error) {
	f.revision++
	w := f.window
	w.SnapshotRevision = f.revision
	o := appcontrol.Observation{Window: w, Display: appcontrol.Display{Resource: a.Resource, Epoch: a.Epoch, ID: w.DisplayID, Virtual: true}, Width: 2, Height: 2, PIDInputVerification: "none", Elements: []appcontrol.Element{{Handle: "toggle", Title: "Qualification PID toggle", Bounds: appcontrol.Bounds{Width: 100, Height: 50}}, {Handle: "editor", Title: "Qualification Unicode input", Value: f.readback.Text, Bounds: appcontrol.Bounds{Width: 600, Height: 60}}, {Handle: "scroll", Title: "Qualification scroll", Bounds: appcontrol.Bounds{Width: 600, Height: 110}}, {Handle: "canvas", Title: "Qualification draggable object", Bounds: appcontrol.Bounds{Width: 600, Height: 110}}}}
	if f.failure == "semantic" {
		o.Elements[0].Press = true
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if f.failure != "pixels" {
		img.Set(0, 0, color.RGBA{R: uint8(f.acts * 35), A: 255})
	}
	var raw bytes.Buffer
	_ = png.Encode(&raw, img)
	o.PNG = raw.Bytes()
	return o, nil
}
func (f *qualificationFake) ActApp(ctx context.Context, r protocol.VscreenActionRequest) (appcontrol.Result, error) {
	if f.frozen {
		return appcontrol.Result{}, errors.New("stale_authority")
	}
	f.acts++
	if f.failure == "permission" || f.failure == "unknown" {
		f.frozen = true
		return appcontrol.Result{}, errors.New(f.failure)
	}
	if f.failure == "cancel" {
		return appcontrol.Result{}, context.Canceled
	}
	if f.failure != "counters_only" {
		switch r.Action.Kind {
		case "click":
			f.readback.Toggle = true
		case "type":
			f.readback.Text = r.Action.Type.Text
			f.readback.SelectionLocation = uint64(len(utf16.Encode([]rune(f.readback.Text))))
		case "key":
			f.readback.Text = "PID 中文 e\u0301"
			f.readback.SelectionLocation--
		case "scroll":
			f.readback.ScrollOffset = 120
		case "drag":
			f.readback.ObjectX += 100
		}
	}
	if err := f.publish(); err != nil {
		return appcontrol.Result{}, err
	}
	if f.failure == "dispatch_only" {
		return appcontrol.Result{Outcome: protocol.VscreenActionDispatched, Mechanism: "pid"}, nil
	}
	return appcontrol.Result{Outcome: protocol.VscreenActionVerified, Mechanism: "pid", CompletionVerified: true}, nil
}
func TestQualificationHarnessActualEffectsNotDeliveryCounters(t *testing.T) {
	for _, failure := range []string{"", "counters_only", "pixels", "semantic", "focus", "permission", "unknown", "cancel", "cleanup", "dispatch_only"} {
		t.Run(failure, func(t *testing.T) {
			base := newInputFixture()
			base.window.Bounds = appcontrol.Bounds{Width: 640, Height: 480}
			base.window.DisplayID = 42
			f := &qualificationFake{inputFixture: base, scope: smokefixture.QualificationScope{Directory: t.TempDir(), Nonce: "private-nonce", BundleID: base.window.Process.BundleID}, failure: failure}
			f.readback = smokefixture.QualificationState{LastProcessedToken: 18446744073709551614, State: smokefixture.State{Nonce: f.scope.Nonce, PID: 123, ProcessStart: "start", WindowID: 9, DisplayID: 42}, ObjectX: 20, ObjectY: 30, AXFocused: failure != "focus", InputSource: "fake-layout"}
			if failure == "cleanup" {
				f.stopFailure = true
			}
			if err := f.publish(); err != nil {
				t.Fatal(err)
			}
			result := &qualificationResult{ForegroundContinuity: "unverified_requires_human_typing_phase"}
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			err := exerciseQualification(ctx, f, f, f.scope, appcontrol.Authority{Epoch: smokeTestEpoch, LeaseEpoch: 1}, result, t.TempDir(), func() (smokefixture.Foreground, error) { return smokefixture.Foreground{PID: 7, WindowID: 8}, nil })
			if (err == nil) != (failure == "") {
				t.Fatalf("failure=%q result=%+v err=%v", failure, result, err)
			}
			if !f.cleanup || !f.frozen {
				t.Fatal("failure did not fence and clean fixture")
			}
			if result.ProductionCertified || result.ForegroundContinuity != "unverified_requires_human_typing_phase" {
				t.Fatal("fabricated production/manual qualification")
			}
			if failure == "" {
				encoded, e := json.Marshal(result)
				if e != nil {
					t.Fatal(e)
				}
				for _, forbidden := range []string{"private-nonce", "nonce", "last_processed_token", "18446744073709551614"} {
					if strings.Contains(string(encoded), forbidden) {
						t.Fatalf("private authority leaked: %s", forbidden)
					}
				}
				if !result.EffectsVerified || !result.OldLeaseRefused || len(result.Stages) != 5 {
					t.Fatal("incomplete effect chain")
				}
				for _, stage := range result.Stages {

					if info, e := os.Stat(stage.Image.Artifact); e != nil || info.Size() == 0 {
						t.Fatal("image evidence missing")
					}
				}
			}
		})
	}
}
func TestQualificationCLIGateBeforeNativeWork(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "")
	var out bytes.Buffer
	if runVscreenInputQualification(&out, t.TempDir()) == nil {
		t.Fatal("missing GUI opt-in accepted")
	}
	if !bytes.Contains(out.Bytes(), []byte(`"status":"blocked"`)) {
		t.Fatal(out.String())
	}
}

func TestQualificationInteractiveFakePromptsAndExplicitManualReceipt(t *testing.T) {
	base := newInputFixture()
	base.window.Bounds = appcontrol.Bounds{Width: 640, Height: 480}
	f := &qualificationFake{inputFixture: base, scope: smokefixture.QualificationScope{Directory: t.TempDir(), Nonce: "private-nonce", BundleID: base.window.Process.BundleID}}
	f.readback = smokefixture.QualificationState{LastProcessedToken: 18446744073709551614, State: smokefixture.State{Nonce: f.scope.Nonce, PID: 123, ProcessStart: "start", WindowID: 9, DisplayID: 42}, ObjectX: 20, ObjectY: 30, AXFocused: true, InputSource: "fake-layout"}
	if err := f.publish(); err != nil {
		t.Fatal(err)
	}
	result := &qualificationResult{ForegroundContinuity: "unverified_requires_human_typing_phase", Manual: qualificationManualEvidence{Status: "unverified"}}
	human := smokefixture.QualificationState{State: smokefixture.State{PID: 700, ProcessStart: "scratch-start", WindowID: 90}, ManualStarted: true}
	var prompts []int
	manual := &qualificationManual{pid: 700, result: &result.Manual, read: func() (smokefixture.QualificationState, error) { return human, nil }, prompt: func(stage int) error {
		prompts = append(prompts, stage)
		human.Text = "ABCDEZ"[:stage]
		human.KeyCount = uint64(stage)
		human.LastKeyNS = time.Now().UnixNano()
		if human.FirstKeyNS == 0 {
			human.FirstKeyNS = human.LastKeyNS
		}
		human.ManualConfirmed = stage == 6
		return nil
	}}
	err := exerciseQualification(t.Context(), f, f, f.scope, appcontrol.Authority{Epoch: smokeTestEpoch, LeaseEpoch: 1}, result, t.TempDir(), func() (smokefixture.Foreground, error) { return smokefixture.Foreground{PID: 700, WindowID: 90}, nil }, manual)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 6 || result.ForegroundContinuity != "verified_manual_fixture_challenge" || result.Manual.Status != "verified_manual_fixture_challenge" || !result.Manual.UserConfirmed {
		t.Fatalf("result=%+v prompts=%v", result, prompts)
	}
}
