//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenCaptureProviderActualHost(t *testing.T) {
	// Given: the hostclient fixture is a real same-binary FD3/4/5 helper.
	executable := filepath.Join(t.TempDir(), "host-fixture")
	command := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", executable, "../vscreen/hostclient")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, output)
	}
	t.Setenv("HOSTCLIENT_TEST_MODE", "media-adapter")
	client, err := hostclient.Start(t.Context(), hostclient.Config{Executable: executable, Build: "test/commit", Media: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	sources, err := client.Sources(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	selected := mirror.EncodedSource{Binding: sources[0].MirrorSourceBinding, DisplayID: sources[0].DisplayID, GeometryRevision: 1, Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31, ShowCursor: true, ExcludedWindowIDs: []uint32{123}}
	provider := VscreenCaptureProvider{Client: client}
	// When
	stream, err := provider.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	sample, err := stream.Next(ctx)
	// Then
	if err != nil || !sample.KeyFrame || len(sample.AnnexB) == 0 {
		t.Fatalf("sample=%+v err=%v", sample, err)
	}
	t.Log("CaptureHub provider -> trusted catalog -> FD5 encoded IDR; negotiated 1280x720/30fps/level31 validated by hostclient")
	t.Run("stale binding rejects without fallback", func(t *testing.T) {
		stale := selected
		stale.GeometryRevision++
		if _, err := provider.Open(t.Context(), stale); !errors.Is(err, native.ErrUnavailable) {
			t.Fatal(err)
		}
	})
}
