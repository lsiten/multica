package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestOwnerSupervisorCancelsDaemonWhenOwnerDies(t *testing.T) {
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	d := &Daemon{cancelFunc: ownerCancel, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.ready.Store(true)
	d.ownerReady.Store(true)
	go d.superviseOwners(ownerCtx, map[string]ownerProbe{
		"gateway": func(context.Context) error { return errors.New("child exited") },
	})
	select {
	case <-ownerCtx.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("owner supervisor did not cancel the daemon")
	}
	if d.ready.Load() || d.ownerReady.Load() {
		t.Fatal("owner supervisor left readiness enabled after child failure")
	}
}
