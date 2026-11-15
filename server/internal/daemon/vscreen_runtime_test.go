package daemon

import (
	"os"
	"testing"
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
