package appcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/vscreen/appclaim"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type controlledBackend struct {
	calls []string
	run   func(context.Context, string, any, any) error
}

func (b *controlledBackend) call(ctx context.Context, op string, in, out any) error {
	b.calls = append(b.calls, op)
	if b.run != nil {
		return b.run(ctx, op, in, out)
	}
	return nil
}
func (b *controlledBackend) close() error { return nil }
func controlFixture(t *testing.T) (*Controller, *controlledBackend, Authority, protocol.VscreenActionRequest) {
	t.Helper()
	a := Authority{Resource: protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: 501}, Epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}, TaskID: "task", TransactionID: "tx", LeaseEpoch: 1}
	d := Display{Resource: a.Resource, Epoch: a.Epoch, ID: 42, Bounds: Bounds{1600, 0, 1600, 900}, Virtual: true}
	b := &controlledBackend{}
	c := controller(Config{Authorize: func(_ context.Context, got Authority, access Access) (Display, error) {
		if got.Resource != a.Resource || got.Epoch != a.Epoch || access == ControlAccess && got.LeaseEpoch != a.LeaseEpoch {
			return Display{}, refusal("stale_authority")
		}
		return d, nil
	}, AuthorizeHuman: func(_ context.Context, r HumanRequest) (Display, error) {
		if r.Grant != "local-one-use" {
			return Display{}, refusal("human_grant_required")
		}
		real := d
		real.Virtual = false
		real.ID = 1
		real.Bounds = Bounds{0, 0, 1600, 900}
		return real, nil
	}}, b)
	w := Window{Handle: "owned", WindowID: 9, DisplayID: 42, Bounds: Bounds{1620, 20, 500, 400}, SnapshotRevision: 1, Process: Process{PID: 123, UID: 501, Start: "process-start", BundleID: "fixture", OSBuild: "test"}}
	c.windows[w.Handle] = &ownedWindow{window: w, display: d}
	r := protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: a.Resource, Epoch: a.Epoch, TaskID: a.TaskID, TransactionID: a.TransactionID, LeaseEpoch: 1, WindowHandle: w.Handle, SnapshotRevision: 1}, ActionID: "one", Sequence: 1, Action: protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "element", Text: "中文 é"}}}
	return c, b, a, r
}
func TestActionRetryNeverDispatchesTwice(t *testing.T) {
	c, b, _, r := controlFixture(t)
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "action" {
			out.(*Result).Outcome = protocol.VscreenActionVerified
			if in.(map[string]any)["CertifiedPID"].(bool) {
				t.Fatal("unreviewed per-PID input enabled")
			}
		}
		return nil
	}
	for range 2 {
		result, err := c.Act(t.Context(), r)
		if err != nil || result.Outcome != protocol.VscreenActionVerified {
			t.Fatal(result, err)
		}
	}
	if len(b.calls) != 1 {
		t.Fatalf("native dispatches=%v", b.calls)
	}
	changed := r
	changed.ActionID = "two"
	changed.Sequence = 2
	if _, err := c.Act(t.Context(), changed); err == nil {
		t.Fatal("action reused consumed snapshot")
	}
}
func TestForgedAuthorityAndSnapshotNeverReachNative(t *testing.T) {
	for _, field := range []string{"runtime", "generation", "lease", "window", "snapshot", "sequence"} {
		t.Run(field, func(t *testing.T) {
			c, b, _, r := controlFixture(t)
			switch field {
			case "runtime":
				r.Target.Resource.RuntimeID = "other"
			case "generation":
				r.Target.Epoch.DisplayGeneration = "other"
			case "lease":
				r.Target.LeaseEpoch++
			case "window":
				r.Target.WindowHandle = "other"
			case "snapshot":
				r.Target.SnapshotRevision++
			case "sequence":
				r.Sequence++
			}
			if _, err := c.Act(t.Context(), r); err == nil || len(b.calls) != 0 {
				t.Fatalf("forged %s dispatched: %v %v", field, err, b.calls)
			}
		})
	}
}
func TestQuiesceCancelsInflightAndWaitsActualBarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, b, a, r := controlFixture(t)
		entered := make(chan struct{})
		done := make(chan error, 1)
		b.run = func(ctx context.Context, op string, _ any, out any) error {
			if op == "action" {
				close(entered)
				<-ctx.Done()
				out.(*Result).Outcome = protocol.VscreenActionVerified
			}
			return nil
		}
		go func() { _, err := c.Act(t.Context(), r); done <- err }()
		<-entered
		if err := c.Quiesce(t.Context(), a.Resource); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err == nil {
			t.Fatal("late verified input accepted")
		}
		if _, err := c.Act(t.Context(), r); err == nil {
			t.Fatal("quiesced authority reused")
		}
		if len(b.calls) != 2 || b.calls[1] != "quiesce" {
			t.Fatal(b.calls)
		}
	})
}
func TestHumanGrantCannotBeSubstitutedByTaskAuthority(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	_, err := c.HumanTransfer(t.Context(), HumanRequest{Resource: a.Resource, Grant: a.TaskID, WindowHandle: "owned", Direction: "to_real"})
	if err == nil {
		t.Fatal("task authority accepted as human grant")
	}
	for _, op := range b.calls {
		if op == "move" {
			t.Fatal("unauthorized move")
		}
	}
	if c.frozen[a.Resource] {
		t.Fatal("invalid human grant revoked task authority")
	}
}
func TestObservationRevalidatesNativeWindowIdentity(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	b.run = func(_ context.Context, op string, _ any, out any) error {
		if op == "observe" {
			raw, _ := json.Marshal(Observation{Window: Window{Handle: "foreign"}, Width: 1, Height: 1})
			return json.Unmarshal(raw, out)
		}
		return nil
	}
	if _, err := c.Observe(t.Context(), a, "owned", false); err == nil {
		t.Fatal("foreign window observation accepted")
	}
}
func TestNativeDeadlineStopsLateVerifiedResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, b, _, r := controlFixture(t)
		b.run = func(ctx context.Context, _ string, _ any, out any) error {
			<-ctx.Done()
			out.(*Result).Outcome = protocol.VscreenActionVerified
			return nil
		}
		start := time.Now()
		result, err := c.Act(t.Context(), r)
		if err == nil || result.Outcome != protocol.VscreenActionUncertain || time.Since(start) != operationLimit {
			t.Fatal(result, err, time.Since(start))
		}
	})
}
func TestDefaultNativePermissionProbeDoesNotPrompt(t *testing.T) {
	permissions, err := ProbePermissions(t.Context())
	var refusal *Error
	if errors.As(err, &refusal) && refusal.Reason == "unsupported_platform" {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("public nonprompt preflight: accessibility=%t screen_recording=%t", permissions.Accessibility, permissions.ScreenRecording)
}

func TestInterruptedDownRetainsClaimAndBlocksHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, b, a, r := controlFixture(t)
		r.Action = protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "A", Modifiers: []string{"meta"}}}
		c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision {
			return PIDInputDecision{Certified: true, InputSourceID: "synthetic-layout"}
		}
		directory := filepath.Join(t.TempDir(), "claims")
		key := appclaim.Key{UID: uint32(os.Getuid()), PID: os.Getpid(), ProcessStartIdentity: "controlled-native-fixture"}
		claim, err := appclaim.Acquire(directory, key)
		if err != nil {
			t.Fatal(err)
		}
		defer claim.ReleaseAfterQuiescence()
		c.windows["owned"].claim = claim
		entered := make(chan struct{})
		finished := make(chan error, 1)
		releaseAttempted := false
		b.run = func(ctx context.Context, op string, input, output any) error {
			switch op {
			case "pid_identity":
				*output.(*Process) = c.windows["owned"].window.Process
				return nil
			case "action":
				if !input.(map[string]any)["CertifiedPID"].(bool) {
					t.Fatal("test did not exercise enabled route")
				}
				close(entered)
				<-ctx.Done()
				releaseAttempted = true
				return refusal("action_uncertain")
			case "quiesce":
				if releaseAttempted {
					return refusal("action_uncertain")
				}
			case "move":
				t.Fatal("unconfirmed up handed window to human")
			}
			return nil
		}
		go func() { _, err := c.Act(t.Context(), r); finished <- err }()
		<-entered
		if err := c.Quiesce(t.Context(), a.Resource); err == nil {
			t.Fatal("unconfirmed release counted as quiescence")
		}
		if err := <-finished; err == nil {
			t.Fatal("interrupted down accepted")
		}
		if err := c.Resume(t.Context(), a); err == nil {
			t.Fatal("new lease passed uncertain input fence")
		}
		if _, err := c.HumanTransfer(t.Context(), HumanRequest{Grant: "local-one-use", Resource: a.Resource, WindowHandle: "owned", Direction: "to_real"}); err == nil {
			t.Fatal("human transfer passed uncertain input fence")
		}
		if err := c.Close(t.Context()); err == nil {
			t.Fatal("close discarded uncertain input claim")
		}
		second, err := appclaim.Acquire(directory, key)
		if err == nil {
			second.ReleaseAfterQuiescence()
			t.Fatal("native claim released before confirmed key-up")
		}
		t.Log("enabled PID route: down -> cancel -> matching-up attempt unconfirmed; quiesce/resume/human/close rejected; real flock retained")
	})
}

func TestReadOnlyObserverCanInspectFrozenWindowButCannotResume(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	c.frozen[a.Resource] = true
	a.TaskID = ""
	a.TransactionID = ""
	a.LeaseEpoch = 0
	a.ObserverGrant = "native-parent-observer"
	b.run = func(_ context.Context, op string, _ any, out any) error {
		if op == "observe" {
			w := c.windows["owned"].window
			w.SnapshotRevision++
			*out.(*Observation) = Observation{Window: w, Width: 500, Height: 400}
		}
		return nil
	}
	if _, err := c.Observe(t.Context(), a, "owned", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(t.Context(), a); err == nil {
		t.Fatal("observer grant resumed input authority")
	}
}
func TestDeniedNativeAXReturnsTypedReasonWithoutOpeningApps(t *testing.T) {
	permissions, err := ProbePermissions(t.Context())
	var nativeError *Error
	if errors.As(err, &nativeError) && nativeError.Reason == "unsupported_platform" {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := newBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	err = b.call(t.Context(), "observe", map[string]any{"Window": map[string]string{"Handle": "nonexistent-fixture-window"}}, nil)
	if !errors.As(err, &nativeError) {
		t.Fatal(err)
	}
	expected := "stale_window"
	if !permissions.Accessibility {
		expected = "accessibility_denied"
	}
	if nativeError.Reason != expected {
		t.Fatalf("native refusal=%s want %s", nativeError.Reason, expected)
	}
	t.Logf("real native guard refused nonexistent fixture before any app action: %s", nativeError.Reason)
}

func TestDisposeReleasesOnlySelectedRuntimeClaim(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	directory := filepath.Join(t.TempDir(), "claims")
	firstKey := appclaim.Key{UID: uint32(os.Getuid()), PID: os.Getpid(), ProcessStartIdentity: "fixture-first"}
	secondKey := firstKey
	secondKey.ProcessStartIdentity = "fixture-second"
	first, err := appclaim.Acquire(directory, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	defer first.ReleaseAfterQuiescence()
	second, err := appclaim.Acquire(directory, secondKey)
	if err != nil {
		t.Fatal(err)
	}
	defer second.ReleaseAfterQuiescence()
	c.windows["owned"].claim = first
	other := *c.windows["owned"]
	other.window.Handle = "sibling"
	other.display.Resource.RuntimeID = "other"
	other.claim = second
	c.windows["sibling"] = &other
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "restore" || op == "forget" {
			if in.(map[string]any)["Window"].(Window).Handle != "owned" {
				t.Fatal("sibling window cleanup")
			}
		}
		return nil
	}
	if err := c.Dispose(t.Context(), a.Resource); err != nil {
		t.Fatal(err)
	}
	if c.windows["owned"] != nil || c.windows["sibling"] == nil {
		t.Fatal("wrong runtime removed")
	}
	acquired, err := appclaim.Acquire(directory, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	acquired.ReleaseAfterQuiescence()
	if acquired, err := appclaim.Acquire(directory, secondKey); err == nil {
		acquired.ReleaseAfterQuiescence()
		t.Fatal("sibling claim released")
	}
	t.Log("runtime A restored/forgotten/released; runtime B window and real flock retained")
}

func TestHumanReturnAllowsFreshObservationButOldInputRemainsFrozen(t *testing.T) {
	c, b, a, action := controlFixture(t)
	virtual := c.windows["owned"].display
	c.config.AuthorizeHuman = func(_ context.Context, r HumanRequest) (Display, error) {
		d := virtual
		if r.Direction == "to_real" {
			d.Virtual = false
			d.ID = 1
			d.Bounds = Bounds{0, 0, 1600, 900}
		}
		return d, nil
	}
	b.run = func(_ context.Context, op string, input, output any) error {
		if op == "move" {
			args := input.(map[string]any)
			w := args["Window"].(Window)
			d := args["Display"].(Display)
			w.DisplayID = d.ID
			w.Bounds = Bounds{d.Bounds.X + 20, d.Bounds.Y + 20, 500, 400}
			*output.(*Window) = w
		}
		if op == "observe" {
			w := c.windows["owned"].window
			w.SnapshotRevision++
			*output.(*Observation) = Observation{Window: w, Width: 500, Height: 400}
		}
		return nil
	}
	if err := c.Quiesce(t.Context(), a.Resource); err != nil {
		t.Fatal(err)
	}
	observer := Authority{Resource: a.Resource, Epoch: a.Epoch, ObserverGrant: "separate-observer"}
	human := HumanRequest{Resource: a.Resource, Grant: "local-one-use", WindowHandle: "owned", Direction: "to_real"}
	if _, err := c.HumanTransfer(t.Context(), human); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Observe(t.Context(), observer, "owned", false); err == nil {
		t.Fatal("read a window still on real display")
	}
	human.Direction = "to_virtual"
	if _, err := c.HumanTransfer(t.Context(), human); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Observe(t.Context(), observer, "owned", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Act(t.Context(), action); err == nil {
		t.Fatal("old input lease thawed on return")
	}
}
