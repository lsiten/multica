//go:build darwin || linux

package appclaim

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"os/exec"
	"testing"
)

func TestAppClaimCrossProcess(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	key := Key{UID: uint32(os.Getuid()), PID: 12345, ProcessStartIdentity: "boot:start1"}
	lock, err := Acquire(directory, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.ReleaseAfterQuiescence(); err != nil {
			t.Error(err)
		}
	}()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAppClaimChild$")
	cmd.Env = append(os.Environ(), "APPCLAIM_CHILD_DIR="+directory)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, out)
	}
	reused := key
	reused.ProcessStartIdentity = "boot:start2"
	other, err := Acquire(directory, reused)
	if err != nil {
		t.Fatal("PID reuse inherited claim", err)
	}
	if err = other.ReleaseAfterQuiescence(); err != nil {
		t.Fatal(err)
	}
	if err = lock.ReleaseAfterQuiescence(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(directory, key)
	if err != nil {
		t.Fatal("claim not released", err)
	}
	if err = next.ReleaseAfterQuiescence(); err != nil {
		t.Fatal(err)
	}
}
func TestAppClaimChild(t *testing.T) {
	directory := os.Getenv("APPCLAIM_CHILD_DIR")
	if directory == "" {
		return
	}
	lock, err := Acquire(directory, Key{UID: uint32(os.Getuid()), PID: 12345, ProcessStartIdentity: "boot:start1"})
	if err == nil {
		if err = lock.ReleaseAfterQuiescence(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("second process acquired same PID")
	}
}

func TestRuntimeClaimCrossProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	lock, err := AcquireRuntime(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.ReleaseAfterQuiescence(); err != nil {
			t.Error(err)
		}
	}()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeClaimChild$")
	cmd.Env = append(os.Environ(), "RUNTIMECLAIM_CHILD_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child contention: %v %s", err, out)
	}
	key.RuntimeID = "other"
	other, err := AcquireRuntime(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.ReleaseAfterQuiescence(); err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeClaimChild(t *testing.T) {
	dir := os.Getenv("RUNTIMECLAIM_CHILD_DIR")
	if dir == "" {
		return
	}
	lock, err := AcquireRuntime(dir, protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "runtime", UID: uint32(os.Getuid())})
	if err == nil {
		if err = lock.ReleaseAfterQuiescence(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("second host acquired same runtime")
	}
}
