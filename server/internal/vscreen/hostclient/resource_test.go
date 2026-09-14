//go:build darwin || linux

package hostclient

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestResourceLifecycleRequiresQuiescenceAndEpoch(t *testing.T) {
	c, err := Start(t.Context(), testConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	request := native.Request{Operation: "ensure", Resource: protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}, Width: 1600, Height: 900}
	ensured, err := c.Call(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Epoch = ensured.Epoch
	request.Operation = "dispose"
	_, err = c.Call(t.Context(), request)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "quiescence_required" {
		t.Fatalf("dispose without quiescence: %v", err)
	}
	request.Operation = "describe"
	stale := request
	stale.Epoch.NativeEpoch = "stale"
	if _, err := c.Call(t.Context(), stale); err == nil {
		t.Fatal("stale host epoch accepted")
	}
	for _, operation := range []string{"describe", "quiesce", "dispose"} {
		request.Operation = operation
		response, err := c.Call(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if response.Epoch != ensured.Epoch {
			t.Fatalf("epoch changed during %s", operation)
		}
	}
}

func TestExplicitInflightCancelReapsHost(t *testing.T) {
	c, err := Start(t.Context(), testConfig(t, "call-stall"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	if _, err := c.Call(ctx, native.Request{Operation: "list"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.exited:
	default:
		t.Fatal("canceled host not reaped")
	}
}
