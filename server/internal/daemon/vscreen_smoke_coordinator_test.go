//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
)

type takeoverProtocolFixture struct {
	path        string
	stopFailure bool
	stopped     bool
}

func (f *takeoverProtocolFixture) BeforeLaunch() {}
func (f *takeoverProtocolFixture) Read() (smokefixture.State, error) {
	var state smokefixture.State
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}
func (f *takeoverProtocolFixture) MarkHumanStage() error {
	s, err := f.Read()
	if err != nil {
		return err
	}
	s.HumanStage = 1
	s.Text = "Multica scripted human handoff"
	return writeSmokePrivateJSON(f.path, s)
}
func (f *takeoverProtocolFixture) Stop(context.Context) error {
	f.stopped = true
	if f.stopFailure {
		return errors.New("owned_fixture_stop_unconfirmed")
	}
	return nil
}

func TestTakeoverSmokeCoordinatorEarlyFailureStopsOnlyOwnedFixture(t *testing.T) {
	t.Setenv(smokePrivateNonceEnv, "")
	for _, fails := range []bool{false, true} {
		fixture := &takeoverProtocolFixture{stopFailure: fails}
		e, err := runTakeoverSmokeCore(t.Context(), VscreenTakeoverSmokeConfig{}, fixture, "owned.fixture", "hash")
		if err == nil || !fixture.stopped || e.FixtureClosed == fails {
			t.Fatalf("failure cleanup evidence=%+v err=%v", e, err)
		}
	}
}
func TestTakeoverSmokeCoordinatorFullSyntheticLifecycle(t *testing.T) {
	result, err := runSyntheticTakeover(t, false)
	if err != nil {
		t.Fatalf("synthetic coordinator: %v; result=%+v", err, result)
	}
	if !result.ProviderStopped || !result.TranscriptDrained || !result.StoppedAck || !result.HumanAck || !result.ReturnAck || !result.FreshObserveBeforeInput || !result.OldLeaseRefused || !result.OldActionRefused || !result.ContinuationCompleted || !result.CleanupAck || !result.Disposed || !result.HostClosed || !result.FixtureClosed {
		t.Fatalf("incomplete coordinator evidence: %+v", result)
	}
	if result.SourceTaskID == result.ContinuationTaskID || result.ReturnReceiptID == "" || len(result.Placements) != 4 || len(result.Images) != 3 {
		t.Fatal("continuation identity/placement/PNG evidence incomplete")
	}
	t.Log("SYNTHETIC ONLY: real daemon runTask twice, same test binary provider entry, owned FD3/5/6 fixture, transfer/return ACKs and actual cleanup receipt; no OS App/display/TCC/model used")
}

func runSyntheticTakeover(t *testing.T, stopFailure bool) (VscreenTakeoverSmokeEvidence, error) {
	t.Helper()
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "1")
	t.Setenv(smokePrivateNonceEnv, strings.Repeat("a", 64))
	t.Setenv("HOME", t.TempDir())
	evidence := t.TempDir()
	private := t.TempDir()
	path := filepath.Join(t.TempDir(), "owned-readback.json")
	t.Setenv("VSCREEN_TAKEOVER_OWNED_READBACK", path)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &takeoverProtocolFixture{path: path, stopFailure: stopFailure}
	return runTakeoverSmokeCore(t.Context(), VscreenTakeoverSmokeConfig{NativeExecutable: executable, NativeBuild: "fixture/commit", EvidenceDir: evidence, PrivateRoot: private}, fixture, "ai.multica.smoke."+strings.Repeat("c", 32), strings.Repeat("b", 64))
}

func TestTakeoverSmokeCoordinatorCleanupFailureCannotPass(t *testing.T) {
	result, err := runSyntheticTakeover(t, true)
	if err == nil || result.FixtureClosed || !result.ContinuationCompleted || !result.CleanupAck || !result.Disposed || !result.HostClosed {
		t.Fatalf("cleanup failure was hidden: %+v %v", result, err)
	}
}
