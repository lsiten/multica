package daemon

import (
	"context"
	"os"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenResourceCanonicalScope(t *testing.T) {
	d := &Daemon{cfg: Config{ServerBaseURL: "https://EXAMPLE.com:443/", NativeHostExecutable: "/test-owned/missing", NativeHostBuild: "test/commit"}, workspaces: map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt"}}}, runtimeIndex: map[string]Runtime{"rt": {ID: "rt"}}}
	key, err := d.vscreenResource("ws", "rt")
	if err != nil || key.BackendIdentity != "https://example.com" || key.UID != uint32(os.Getuid()) {
		t.Fatalf("canonical resource: %+v %v", key, err)
	}
	if _, err = d.vscreenResource("foreign", "rt"); err == nil {
		t.Fatal("foreign workspace authorized")
	}
}

func TestVscreenDisabledDoesNotStartNativeHost(t *testing.T) {
	d := &Daemon{cfg: Config{ServerBaseURL: "https://example.com", NativeHostExecutable: "/test-owned/missing", NativeHostBuild: "test/commit"}, workspaces: map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt"}}}, runtimeIndex: map[string]Runtime{"rt": {ID: "rt"}}}
	state, err := d.vscreenSnapshot(context.Background(), "ws", "rt")
	if err != nil || state.State != protocol.VscreenStateDisabled || state.Permissions.ScreenRecording != "unknown" || state.StateRevision == 0 {
		t.Fatalf("disabled: %+v %v", state, err)
	}
	if d.vscreen != nil && d.vscreen.client != nil {
		t.Fatal("disabled state spawned native helper")
	}
}
