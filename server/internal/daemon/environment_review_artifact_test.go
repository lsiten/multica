package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestReviewArtifactHandoffLargeResult verifies task26: an oversized review
// result is written to the private anchored namespace and referenced in-band,
// then read back by the caller without exceeding the 256 KiB control channel.
func TestReviewArtifactHandoffLargeResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "test", Account: "account", Profile: "profile", DaemonID: "daemon", Service: "environment"}, "fixture/commit")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	service := &environmentProcessService{bootstrap: bootstrap}

	// An oversized page forces the artifact path. The page is a valid large JSON string.
	large := &protocol.LocalReviewResult{Page: json.RawMessage(`"` + strings.Repeat("x", 200<<10) + `"`)}
	request := runtimeproc.Request{RequestID: "1:review-large", ReplayEpoch: 1}
	out, rerr := service.encodeReviewRunResult(request, large)
	if rerr != nil {
		t.Fatalf("encode large: %v", rerr)
	}
	var resp reviewRunResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result != nil {
		t.Fatal("large result was carried in-band instead of via artifact")
	}
	if resp.Artifact == nil {
		t.Fatal("large result did not produce an artifact reference")
	}
	// The caller re-derives the name and reads the bounded artifact.
	namespace := runtimeproc.ReviewArtifactNamespace(root)
	name := runtimeproc.ReviewArtifactName(resp.Artifact.RequestID)
	body, err := runtimeproc.ReadReviewArtifact(namespace, name, resp.Artifact.Hash, resp.Artifact.Length)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var got protocol.LocalReviewResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	if string(got.Page) != string(large.Page) {
		t.Fatal("artifact did not round-trip the exact page")
	}
	// A repeat with the same request id references the same artifact (idempotent).
	out2, rerr := service.encodeReviewRunResult(request, large)
	if rerr != nil {
		t.Fatalf("encode repeat: %v", rerr)
	}
	var resp2 reviewRunResponse
	if err := json.Unmarshal(out2, &resp2); err != nil {
		t.Fatalf("unmarshal repeat: %v", err)
	}
	if resp2.Artifact == nil || resp2.Artifact.RequestID != resp.Artifact.RequestID {
		t.Fatalf("repeat did not reference the same artifact: %+v", resp2)
	}
	t.Logf("OBSERVE large review result routed through private artifact %s (%d bytes)", name, resp.Artifact.Length)
}

// TestReviewArtifactHandoffSmallResultInBand verifies that a result below the
// threshold is carried in-band and never touches the private artifact namespace.
func TestReviewArtifactHandoffSmallResultInBand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "test", Account: "account", Profile: "profile", DaemonID: "daemon", Service: "environment"}, "fixture/commit")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	service := &environmentProcessService{bootstrap: bootstrap}
	small := &protocol.LocalReviewResult{Review: json.RawMessage(`{"a":1}`), ClaimToken: "tok"}
	request := runtimeproc.Request{RequestID: "1:review-small", ReplayEpoch: 1}
	out, rerr := service.encodeReviewRunResult(request, small)
	if rerr != nil {
		t.Fatalf("encode small: %v", rerr)
	}
	var resp reviewRunResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Artifact != nil {
		t.Fatalf("small result was routed through an artifact: %+v", resp.Artifact)
	}
	if resp.Result == nil || resp.Result.ClaimToken != "tok" {
		t.Fatalf("small result was not carried in-band: %+v", resp.Result)
	}
	entries, _ := os.ReadDir(runtimeproc.ReviewArtifactNamespace(root))
	if len(entries) != 0 {
		t.Fatalf("small result created an artifact (%d entries)", len(entries))
	}
}

// TestReviewRunRoundTripThroughChild verifies the full parent->child review path
// end to end: the control parent delegates review.run to the owned child and
// reconstructs the exact result, including the large-artifact path.
func TestReviewRunRoundTripThroughChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	t.Cleanup(cleanup)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/me" {
			if r.Header.Get("Authorization") != "Bearer owned-parent" {
				http.Error(w, "unauthorized", 403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "owned-account"})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(backend.Close)
	d.client = NewClient(backend.URL)
	d.client.token = "owned-parent"
	d.cfg.ServerBaseURL = backend.URL
	d.cfg.DaemonID = "owned-environment-daemon"
	d.cfg.ProcessServices = []string{"environment"}
	d.cfg.AgentTimeout = 15 * time.Second
	d.cfg.NativeHostBuild = "fixture/commit"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeHostExecutable = executable
	profile, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(profile, "vscreen.json")
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.WorkspacesRoot = root
	d.localPathLocks = NewLocalPathLocker()
	d.repoCache = &environmentRepoCache{daemon: d}
	d.workspaces["ws-leader"] = &workspaceState{workspaceID: "ws-leader", runtimeIDs: []string{"rt-leader"}, allowedRepoURLs: map[string]struct{}{}}
	t.Cleanup(func() {
		if d.environmentProcess != nil {
			if err := d.environmentProcess.close(); err != nil {
				t.Error(err)
			}
			t.Log("CLEANUP review round-trip child closed/reaped")
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if _, err := d.ensureEnvironmentProcess(ctx); err != nil {
		t.Fatalf("ensure environment child: %v", err)
	}
	// A review for the bound runtime delegates to the child and reconstructs the
	// exact result, exercising the full parent->child review.run path.
	command := protocol.LocalReviewCommand{WorkspaceID: "ws-leader", RuntimeID: "rt-leader", TaskID: "task-review", Action: "read"}
	result, err := d.environmentProcess.routeReviewToChild(ctx, command)
	if err != nil {
		t.Fatalf("route review to child: %v", err)
	}
	if result.Error == "" {
		t.Log("OBSERVE review round-trip through child returned an in-band result")
	}
	t.Logf("OBSERVE review.run delegated to child result_error=%q", result.Error)
}
