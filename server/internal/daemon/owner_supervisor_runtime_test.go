//go:build darwin || linux

package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

// TestOwnerSupervisorCancelsDaemonWhenRuntimeChildExits drives the liveness
// boundary with a real runtimeproc child. It protects the user-visible
// contract that a dead required owner makes the daemon stop claiming work and
// lets the desktop restart a clean topology; the unit probe test alone would
// not catch a broken runtime transport or a child that failed to terminate.
func TestOwnerSupervisorCancelsDaemonWhenRuntimeChildExits(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	process, _ := startRuntimeChild(t, "gateway", filepath.Join(parent, "gateway"), parent)
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	d := &Daemon{cancelFunc: ownerCancel, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.ready.Store(true)
	d.ownerReady.Store(true)
	go d.superviseOwners(ownerCtx, map[string]ownerProbe{
		"gateway": func(ctx context.Context) error { return probeRuntimeOwner(ctx, process) },
	})

	if err := process.Close(); err != nil {
		t.Fatalf("close runtime child: %v", err)
	}
	select {
	case <-ownerCtx.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("owner supervisor did not cancel after the runtime child exited")
	}
	if d.ready.Load() || d.ownerReady.Load() {
		t.Fatal("owner supervisor left readiness enabled after the runtime child exited")
	}
}
