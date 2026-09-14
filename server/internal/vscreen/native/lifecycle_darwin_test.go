//go:build darwin && cgo && nativeintegration

package native

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"golang.org/x/sys/unix"
)

func TestLifecycleRealDisplay(t *testing.T) {
	// Given: an explicitly selected task-owned same-binary host, with no app windows.
	binary := os.Getenv("MULTICA_VSCREEN_SMOKE_BINARY")
	evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if binary == "" || evidence == "" {
		t.Fatal("explicit binary and evidence paths required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	trace := make(map[string]Response)
	defer func() {
		body, err := json.MarshalIndent(trace, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(filepath.Join(evidence, "task-2-lifecycle.json"), body, 0600); err != nil {
			t.Error(err)
		}
	}()
	start := func() (net.Conn, *exec.Cmd) {
		descriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		parentFile := os.NewFile(uintptr(descriptors[0]), "parent")
		parent, err := net.FileConn(parentFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := parentFile.Close(); err != nil {
			t.Fatal(err)
		}
		child := os.NewFile(uintptr(descriptors[1]), "child")
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, binary, "internal-vscreen-host")
		command.ExtraFiles = []*os.File{child, reader}
		command.Stderr = os.Stderr
		command.Env = append(os.Environ(), "HOME="+t.TempDir())
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		if err := child.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		var token [32]byte
		rand.Read(token[:])
		if _, err := writer.Write(token[:]); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := parent.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(parent, Request{Version: 1, Build: "dev/unknown", ID: "hello", Operation: "hello", Token: token[:]}); err != nil {
			t.Fatal(err)
		}
		var hello Response
		if err := ReadMessage(parent, &hello); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			parent.Close()
			if command.ProcessState == nil {
				command.Wait()
			}
		})
		return parent, command
	}
	parent, command := start()
	key := protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "native-smoke", RuntimeID: "lifecycle", UID: uint32(os.Getuid())}
	request := func(label, operation string, epoch protocol.VscreenEpoch) Response {
		if err := WriteMessage(parent, Request{Version: 1, Build: "dev/unknown", ID: label, Operation: operation, Resource: key, Epoch: epoch, Width: 800, Height: 600}); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := ReadMessage(parent, &response); err != nil {
			t.Fatal(err)
		}
		trace[label] = response
		if response.Error != "" && label != "helper_death_after" {
			observer, observerCommand := start()
			if err := WriteMessage(observer, Request{Version: 1, Build: "dev/unknown", ID: "observer", Operation: "list"}); err != nil {
				t.Fatal(err)
			}
			var observed Response
			if err := ReadMessage(observer, &observed); err != nil {
				t.Fatal(err)
			}
			trace["independent_after_dispose"] = observed
			observer.Close()
			observerCommand.Wait()
			t.Fatalf("%s: %s", label, response.Error)
		}
		return response
	}
	before := request("before", "list", protocol.VscreenEpoch{})
	// When: create and repeat Ensure, then explicitly quiesce and dispose.
	created := request("created", "ensure", protocol.VscreenEpoch{})
	contender, contenderCommand := start()
	if err := WriteMessage(contender, Request{Version: 1, Build: "dev/unknown", ID: "contender", Operation: "ensure", Resource: key, Width: 800, Height: 600}); err != nil {
		t.Fatal(err)
	}
	var denied Response
	if err := ReadMessage(contender, &denied); err != nil {
		t.Fatal(err)
	}
	trace["cross_profile_claim"] = denied
	if denied.Error != "runtime_claim_unavailable" || denied.Display != nil {
		t.Fatalf("second host acquired runtime display: %+v", denied)
	}
	if err := contender.Close(); err != nil {
		t.Fatal(err)
	}
	if err := contenderCommand.Wait(); err != nil {
		t.Fatal(err)
	}
	repeated := request("repeated", "ensure", protocol.VscreenEpoch{})
	if created.Display == nil || repeated.Display == nil || created.Display.ID != repeated.Display.ID || created.Epoch != repeated.Epoch {
		t.Fatal("Ensure was not idempotent")
	}
	during := request("during", "list", protocol.VscreenEpoch{})
	if len(during.Displays) != len(before.Displays)+1 {
		t.Fatal("system enumeration did not gain exactly one display")
	}
	for _, original := range before.Displays {
		found := false
		for _, current := range during.Displays {
			if current.ID == original.ID {
				found = true
				if current != original {
					t.Fatal("existing display geometry, primary identity or mirroring changed during lifecycle")
				}
			}
		}
		if !found {
			t.Fatal("existing display missing while virtual display active")
		}
	}
	if created.Display.Main || created.Display.MirrorOf != 0 {
		t.Fatal("virtual display became primary or mirrored")
	}
	request("quiesced", "quiesce", created.Epoch)
	request("disposed", "dispose", created.Epoch)
	after := request("after", "list", protocol.VscreenEpoch{})
	// Then: original system displays and arrangement are unchanged.
	if !reflect.DeepEqual(before.Displays, after.Displays) {
		t.Fatal("display enumeration not restored")
	}
	orphan := request("parent_death_created", "ensure", protocol.VscreenEpoch{})
	if orphan.Display == nil {
		t.Fatal("missing owned display")
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	parent, command = start()
	final := request("parent_death_after", "list", protocol.VscreenEpoch{})
	if !reflect.DeepEqual(before.Displays, final.Displays) {
		t.Fatal("parent EOF leaked display or changed arrangement")
	}
	crash := request("helper_death_created", "ensure", protocol.VscreenEpoch{})
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed helper unexpectedly exited successfully")
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	parent, command = start()
	var reclaimed Response
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		reclaimed = request("helper_death_after", "list", protocol.VscreenEpoch{})
		if reclaimed.Error == "" && reflect.DeepEqual(before.Displays, reclaimed.Displays) {
			break
		}
	}
	if reclaimed.Error != "" || !reflect.DeepEqual(before.Displays, reclaimed.Displays) {
		t.Fatal("helper death leaked a display or changed original monitors")
	}
	if err := WriteMessage(parent, Request{Version: 1, Build: "dev/unknown", ID: "old-handle", Operation: "describe", Resource: key, Epoch: crash.Epoch}); err != nil {
		t.Fatal(err)
	}
	var stale Response
	if err := ReadMessage(parent, &stale); err != nil {
		t.Fatal(err)
	}
	trace["old_handle_rejected"] = stale
	if stale.Error == "" || stale.Display != nil || stale.Epoch.NativeEpoch == crash.Epoch.NativeEpoch {
		t.Fatal("old helper handle was accepted")
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Logf("real display %d; screen recording=%v; SCK catalog visible=%v", created.Display.ID, created.Display.ScreenRecording, created.Display.CaptureVisible)
}
