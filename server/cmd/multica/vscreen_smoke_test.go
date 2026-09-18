package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type smokeFixture struct {
	calls                                       []string
	missingSource, cleanupFailure, videoFailure bool
}

var smokeTestEpoch = protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}
var smokeTestDisplay = native.Display{ID: 42, UUID: "fixture", Managed: true}

func (f *smokeFixture) Call(_ context.Context, request native.Request) (native.Response, error) {
	f.calls = append(f.calls, request.Operation)
	if request.Operation == "quiesce" && f.cleanupFailure {
		return native.Response{}, errors.New("fixture_quiesce_failed")
	}
	if request.Operation == "list" {
		if len(f.calls) == 2 {
			return native.Response{Displays: []native.Display{smokeTestDisplay}}, nil
		}
		return native.Response{}, nil
	}
	return native.Response{Display: &smokeTestDisplay, Epoch: smokeTestEpoch, Quiescent: true}, nil
}
func (f *smokeFixture) Sources(_ context.Context, key protocol.ResourceKey) ([]native.SourceDescriptor, error) {
	f.calls = append(f.calls, "sources")
	if f.missingSource {
		return nil, nil
	}
	return []native.SourceDescriptor{{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: key, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "fixture"}, NativeEpoch: "native", Generation: "display"}, DisplayID: 42, GeometryRevision: 1}}, nil
}
func (f *smokeFixture) Close() error { f.calls = append(f.calls, "close"); return nil }

func TestVscreenSmokeFixtureLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fixture   smokeFixture
		scenario  string
		wantError bool
	}{
		{name: "lifecycle", scenario: "lifecycle"},
		{name: "source", scenario: "source"},
		{name: "missing source cleans display", fixture: smokeFixture{missingSource: true}, scenario: "source", wantError: true},
		{name: "cleanup failure never passes", fixture: smokeFixture{cleanupFailure: true}, scenario: "lifecycle", wantError: true},
		{name: "video failure cleans display", fixture: smokeFixture{videoFailure: true}, scenario: "video", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := vscreenSmokeResult{Scenario: tc.scenario}
			err := exerciseSmokeDisplay(context.Background(), &tc.fixture, protocol.ResourceKey{}, &result, func(native.SourceDescriptor) (*smokeVideoResult, error) {
				if tc.fixture.videoFailure {
					return nil, errors.New("fixture_video_failed")
				}
				return nil, nil
			})
			if (err != nil) != tc.wantError || !result.HostClosed || result.Disposed == tc.fixture.cleanupFailure {
				t.Fatalf("error=%v result=%+v", err, result)
			}
			want := []string{"ensure", "list", "sources", "quiesce", "dispose", "list", "close"}
			if tc.fixture.cleanupFailure {
				want = []string{"ensure", "list", "sources", "quiesce", "close"}
			}
			if !reflect.DeepEqual(tc.fixture.calls, want) {
				t.Fatalf("calls=%v want=%v", tc.fixture.calls, want)
			}
		})
	}
}

func TestVscreenSmokeRequiresExplicitGUIOptIn(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "")
	var output bytes.Buffer
	if err := runVscreenSmoke(&output, "lifecycle", t.TempDir()); err == nil || !bytes.Contains(output.Bytes(), []byte("gui_not_authorized")) || bytes.Contains(output.Bytes(), []byte(`"gui_exercised":true`)) {
		t.Fatalf("error=%v output=%s", err, &output)
	}
}

func TestVscreenSmokeNALTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"SPS PPS IDR", []byte{0, 0, 0, 1, 0x67, 1, 0, 0, 0, 1, 0x68, 1, 0, 0, 0, 1, 0x65, 1}, true},
		{"inter frame", []byte{0, 0, 0, 1, 0x41, 1}, true},
		{"not Annex B", []byte{1, 2, 3}, false},
		{"no slice", []byte{0, 0, 0, 1, 0x67, 1}, false},
		{"invalid header", []byte{0, 0, 0, 1, 0xff, 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := smokeNALTypes(tc.data)
			if (err == nil) != tc.valid {
				t.Fatalf("error=%v valid=%v", err, tc.valid)
			}
		})
	}
}
