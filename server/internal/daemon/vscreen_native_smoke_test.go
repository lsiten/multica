//go:build darwin && cgo && vscreenintegration

package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenProductionNativeReadOnlyProbe(t *testing.T) {
	if os.Getenv("MULTICA_RUN_VSCREEN_NATIVE_SMOKE") != "1" {
		t.Skip("explicit native smoke opt-in required")
	}
	binary := filepath.Join(t.TempDir(), "multica")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/multica")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	if !native.Supported() {
		t.Fatal("virtual screen selectors unavailable")
	}
	client, err := hostclient.Start(t.Context(), hostclient.Config{Executable: binary, Build: "dev/unknown", Media: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	key := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "native-smoke", RuntimeID: "readonly", UID: uint32(os.Getuid())}
	sources, err := client.Sources(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Call(t.Context(), native.Request{Operation: "list"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production helper authenticated; sources=%d displays=%d; no ensure/dispose or permission prompt", len(sources), len(response.Displays))
	readback, err := json.Marshal(response.Displays)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native display readback=%s", readback)
	if len(sources) == 0 {
		t.Fatal("no system source readback")
	}
	selected := sources[0]
	recording := false
	for _, display := range response.Displays {
		if display.ID == selected.DisplayID {
			recording = display.ScreenRecording
		}
	}
	if !recording {
		_, err = (VscreenCaptureProvider{Client: client}).Open(t.Context(), mirror.EncodedSource{Binding: selected.MirrorSourceBinding, DisplayID: selected.DisplayID, GeometryRevision: selected.GeometryRevision, Width: 1280, Height: 720, FPS: 30, Bitrate: 8000000, MaxLevelIDC: 31})
		if !errors.Is(err, mirror.ErrCapturePermissionDenied) {
			t.Fatalf("recording=false must produce typed permission denial, got %v", err)
		}
		t.Log("recording=false -> permission-denied; real capture acceptance remains blocked by TCC")
		if !mirror.ScreenCapturePermissionGranted() {
			if _, err := (mirror.NativeCapturer{NoPermissionPrompt: true}).Capture(t.Context()); !errors.Is(err, mirror.ErrCapturePermissionDenied) {
				t.Fatalf("managed JPEG preflight failed: %v", err)
			}
			t.Log("managed JPEG nonprompting preflight denied before capture")
		}
	}
}
