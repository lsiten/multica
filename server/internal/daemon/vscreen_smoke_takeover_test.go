package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTakeoverSmokeNoOptInHasNoSideEffects(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "")
	root := filepath.Join(t.TempDir(), "not-created")
	config := VscreenTakeoverSmokeConfig{NativeExecutable: "/owned/missing", NativeBuild: "fixture", EvidenceDir: root}
	if _, err := LaunchVscreenTakeoverSmoke(t.Context(), config); err == nil || err.Error() != "gui_not_authorized" {
		t.Fatal(err)
	}
	if _, err := RunVscreenTakeoverSmoke(t.Context(), config); err == nil || err.Error() != "gui_not_authorized" {
		t.Fatal(err)
	}
	if err := RunVscreenSmokeProvider(t.Context(), filepath.Join(root, "config"), nil, strings.NewReader(""), &bytes.Buffer{}); err == nil || err.Error() != "gui_not_authorized" {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("GUI gate performed filesystem work")
	}
}
func TestTakeoverSmokePrivateScopeRefusesForgery(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "1")
	t.Setenv(smokeProviderNonceEnv, strings.Repeat("a", 64))
	t.Setenv("MULTICA_TASK_ID", "owned")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace")
	path := filepath.Join(t.TempDir(), "provider.json")
	config := smokeProviderConfig{GUIAuthorized: true, Nonce: strings.Repeat("a", 64), OwnerPID: os.Getppid(), TaskID: "owned", WorkspaceID: "workspace", RuntimeID: "runtime", BundleID: "ai.multica.smoke." + strings.Repeat("c", 32), Stage: "source", EvidenceDir: t.TempDir()}
	for _, mutate := range []func(*smokeProviderConfig){func(c *smokeProviderConfig) { c.Nonce = strings.Repeat("b", 64) }, func(c *smokeProviderConfig) { c.OwnerPID++ }, func(c *smokeProviderConfig) { c.TaskID = "other" }, func(c *smokeProviderConfig) { c.WorkspaceID = "other" }, func(c *smokeProviderConfig) { c.Stage = "shell" }, func(c *smokeProviderConfig) { c.GUIAuthorized = false }, func(c *smokeProviderConfig) { c.BundleID = "com.apple.Safari" }} {
		forged := config
		mutate(&forged)
		if err := writeSmokePrivateJSON(path, forged); err != nil {
			t.Fatal(err)
		}
		if err := RunVscreenSmokeProvider(t.Context(), path, nil, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatal("forged provider accepted")
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := readSmokePrivateJSON(path, &config); err == nil {
		t.Fatal("public private-config accepted")
	}
}

type placementFixture struct{ state smokefixture.State }

func (f placementFixture) Read() (smokefixture.State, error) { return f.state, nil }
func TestTakeoverSmokePlacementUsesRealReadback(t *testing.T) {
	source := native.SourceDescriptor{DisplayID: 42, LogicalWidth: 1600, LogicalHeight: 900, X: 2000, Y: 100}
	state := smokefixture.State{PID: 123, ProcessStart: "owned-start", WindowID: 9, DisplayID: 42, Bounds: smokefixture.WindowBounds{X: 2010, Y: 110, Width: 640, Height: 440}, HumanStage: 1, Text: "human"}
	if _, err := awaitTakeoverPlacement(t.Context(), placementFixture{state}, source, 1, "human"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*smokefixture.State){func(s *smokefixture.State) { s.DisplayID = 1 }, func(s *smokefixture.State) { s.Bounds.X = 0 }, func(s *smokefixture.State) { s.Bounds.Width = 0 }, func(s *smokefixture.State) { s.HumanStage = 0 }, func(s *smokefixture.State) { s.Text = "old" }, func(s *smokefixture.State) { s.Closed = true }} {
		invalid := state
		mutate(&invalid)
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		_, err := awaitTakeoverPlacement(ctx, placementFixture{invalid}, source, 1, "human")
		cancel()
		if err == nil {
			t.Fatal("unobserved placement/human change accepted")
		}
	}
}
func TestTakeoverSmokeEventOrderAndAuthentication(t *testing.T) {
	b, err := newTakeoverSmokeBackend(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.sourceID = "source"
	b.continuationID = "next"
	source := smokeProviderEvent{TaskID: "source", Stage: "source-observed", Window: "window", Revision: 1, Element: "text"}
	if err = b.event(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	next := smokeProviderEvent{TaskID: "next", Stage: "continuation-input", Window: "window", Revision: 2}
	if err = b.event(t.Context(), next); err == nil {
		t.Fatal("input preceded new observation")
	}
	next.Stage = "continuation-observed"
	if err = b.event(t.Context(), next); err == nil {
		t.Fatal("observation accepted before return ACK")
	}
	b.versions[protocol.VscreenInterventionReadyToContinue] = 3
	if err = b.event(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	next.Stage = "continuation-input"
	if err = b.event(t.Context(), next); err != nil || !b.freshObserveBeforeInput {
		t.Fatal(err)
	}
	response, err := http.Get(b.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("unauthenticated smoke backend accepted")
	}
}
func TestTakeoverSmokeProviderDoesNotCallNonLoopbackMCP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	raw := []byte(`{"mcpServers":{"multica-vscreen":{"url":"https://example.invalid"}}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	err := runSmokeProvider(t.Context(), smokeProviderConfig{Endpoint: "http://127.0.0.1:1"}, []string{"--mcp-config", path}, strings.NewReader("prompt\n"), &bytes.Buffer{})
	if err == nil || err.Error() != "smoke_loopback_required" {
		t.Fatal(err)
	}
}
func TestTakeoverSmokeMCPRejectsMissingImageAndInvalidPNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "{}"}}}})
	}))
	defer server.Close()
	_, pixels, err := callSmokeMCP(t.Context(), server.URL, "vscreen_observe", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err = writeTakeoverPNG(t.TempDir(), "observation.png", pixels); err == nil {
		t.Fatal("missing PNG accepted")
	}
}
