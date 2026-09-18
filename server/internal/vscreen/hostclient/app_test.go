//go:build darwin || linux

package hostclient

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func appAuthority(c *Client) appcontrol.Authority {
	return appcontrol.Authority{Resource: protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}, Epoch: protocol.VscreenEpoch{NativeEpoch: c.NativeEpoch(), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}, TaskID: "task", TransactionID: "transaction", LeaseEpoch: 1}
}
func TestAppOwnedChildCancellationKeepsSiblingAndControlAlive(t *testing.T) {
	cfg := testConfig(t, "app")
	cfg.AppControl = true
	c, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := appAuthority(c)
	if err := c.Grant(t.Context(), a, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := c.LaunchApp(ctx, a, appcontrol.LaunchRequest{BundleID: "blocked"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel result %v", err)
	}
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
		t.Fatalf("FD3 died: %v", err)
	}
	a.Resource.RuntimeID = "sibling"
	if w, err := c.LaunchApp(t.Context(), a, appcontrol.LaunchRequest{BundleID: "sibling"}); err != nil || w.Handle != "owned" {
		t.Fatalf("sibling app: %+v %v", w, err)
	}
	if _, err := c.ObserveApp(t.Context(), a, "owned", true); err != nil {
		t.Fatalf("FD5 died: %v", err)
	}
}
func TestAppSnapshotResponseBulkOrderingAndIntegrity(t *testing.T) {
	for _, mode := range []string{"app-bulk-first", "app-response-first", "app-bad-hash", "app-oversize", "app-wrong-identity", "app-reordered", "app-eof"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t, mode)
			cfg.AppControl = true
			c, err := Start(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			o, err := c.ObserveApp(t.Context(), appAuthority(c), "owned", true)
			valid := mode == "app-bulk-first" || mode == "app-response-first"
			if valid && (err != nil || len(o.PNG) <= native.SnapshotChunkBytes) {
				t.Fatalf("image missing bytes=%d err=%v", len(o.PNG), err)
			}
			if !valid && (err == nil || len(o.PNG) != 0) {
				t.Fatalf("invalid image exposed bytes=%d err=%v", len(o.PNG), err)
			}
			if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
				t.Fatalf("bulk failure killed FD3: %v", err)
			}
		})
	}
}
func TestSnapshotLateCancelledAndDuplicateChunksCannotAllocateOrReplay(t *testing.T) {
	c := &Client{snapshots: make(map[string]*pendingSnapshot)}
	sample := native.MediaSample{Kind: native.MediaSnapshot, StreamID: strings.Repeat("a", 32), SnapshotTotal: 8, PNG: []byte("12345678")}
	c.acceptSnapshot(sample)
	if len(c.snapshots) != 0 {
		t.Fatal("unknown sample allocated")
	}
	p := &pendingSnapshot{done: make(chan struct{})}
	c.snapshots[sample.StreamID] = p
	c.acceptSnapshot(sample)
	c.acceptSnapshot(sample)
	if !errors.Is(p.err, native.ErrProtocol) {
		t.Fatal("duplicate chunk accepted")
	}
	delete(c.snapshots, sample.StreamID)
	c.acceptSnapshot(sample)
	if len(c.snapshots) != 0 {
		t.Fatal("late cancelled sample resurrected")
	}
}
