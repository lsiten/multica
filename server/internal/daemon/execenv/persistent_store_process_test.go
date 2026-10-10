package execenv

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUseProcessHelper(t *testing.T) {
	if os.Getenv("MULTICA_OWNED_STORE_HELPER") != "1" {
		return
	}
	lease, err := UsePersistentStore(context.Background(), os.Getenv("MULTICA_OWNED_STORE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(context.Background(), nil)
	if _, err = os.Stdout.Write([]byte("READY\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentStoreKernelLeaseSurvivesControllerAndReleasesOnCrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(t.TempDir(), "private-session")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStoreUseProcessHelper$", "-test.count=1")
	child.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "MULTICA_OWNED_STORE_HELPER=1", "MULTICA_OWNED_STORE_PATH=" + store}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("REGISTER owned store helper executable=%s store=%s home=%s", executable, store, home)
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		stdin.Close()
		if !reaped {
			child.Process.Kill()
			child.Wait()
		}
	}()
	ready := make([]byte, 6)
	if _, err = io.ReadFull(output, ready); err != nil || string(ready) != "READY\n" {
		t.Fatalf("owned helper not ready: %q %v", ready, err)
	}
	t.Logf("OBSERVE store participant held by child PID=%d", child.Process.Pid)
	if release, ok, err := ReservePersistentStoreDeletion(ctx, store); err != nil || ok {
		if release != nil {
			release()
		}
		t.Fatalf("controller could delete child-held store: %v %v", ok, err)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("expected killed owned helper")
	}
	reaped = true
	release, ok, err := ReservePersistentStoreDeletion(ctx, store)
	if err != nil || !ok {
		t.Fatalf("kernel did not release dead child's participant: %v %v", ok, err)
	}
	release()
	t.Logf("CLEANUP child PID=%d reaped; deletion lock acquired only after actual process exit", child.Process.Pid)
}
