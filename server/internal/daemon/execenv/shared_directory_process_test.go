package execenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedDirectoryLeaseProcessHelper(t *testing.T) {
	path := os.Getenv("MULTICA_SHARED_LEASE_HELPER_PATH")
	if path == "" {
		return
	}
	lease, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MULTICA_SHARED_LEASE_HELPER_READY"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(30 * time.Second):
	case <-t.Context().Done():
	}
	if err := lease.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSharedDirectoryLeaseSurvivesAnotherDaemonAndRecoversCrash(t *testing.T) {
	path := t.TempDir()
	first, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Finish(context.Background(), nil); err != nil {
			t.Error(err)
		}
	})
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSharedDirectoryLeaseProcessHelper$")
	cmd.Env = append(os.Environ(), "MULTICA_SHARED_LEASE_HELPER_PATH="+path, "MULTICA_SHARED_LEASE_HELPER_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("second daemon did not publish its shared lease")
		case <-poll.C:
		}
	}
	if err := first.Finish(t.Context(), func(last bool) error {
		if last {
			t.Error("first daemon ignored live second process")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, TaskContextMarkerRelPath)); err != nil {
		t.Fatal("guard removed while another process was active")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	third, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatalf("dead daemon lease prevented recovery: %v", err)
	}
	if err := third.Finish(t.Context(), func(last bool) error {
		if !last {
			t.Error("dead process retained live ownership")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, TaskContextMarkerRelPath)); !os.IsNotExist(err) {
		t.Fatal("recovered last process did not restore the directory")
	}
}

func TestCancelledSharedSettlementReleasesKernelOwnership(t *testing.T) {
	path := t.TempDir()
	lease, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := lease.Finish(ctx, nil); err == nil {
		t.Fatal("cancelled settlement succeeded")
	}
	next, err := UseSharedDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Finish(t.Context(), func(last bool) error {
		if !last {
			t.Error("cancelled run leaked kernel ownership")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
