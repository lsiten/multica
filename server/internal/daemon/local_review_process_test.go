package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This helper executes only in a test subprocess, never through an installed agent CLI.
func TestLocalReviewRuntimeProcessHelper(t *testing.T) {
	if os.Getenv("MULTICA_REVIEW_SUBPROCESS") != "1" {
		t.Skip("subprocess only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	d := New(Config{WorkspacesRoot: os.Getenv("MR_TEST_ROOT")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client = NewClient(os.Getenv("MR_TEST_SERVER"))
	d.client.SetToken(os.Getenv("MR_TEST_TOKEN"))
	runtimeID := os.Getenv("MR_TEST_RUNTIME")
	d.runtimeIndex = map[string]Runtime{runtimeID: {ID: runtimeID}}
	var claim protocol.LocalReviewClaim
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for claim.Command == nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			if err := d.client.postJSON(ctx, "/runtime/"+runtimeID+"/claim", struct{}{}, &claim); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, report := d.runClaimedReview(ctx, *claim.Command)
	if !report {
		t.Fatal("live reader was cancelled")
	}
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := d.client.postJSON(ctx, "/runtime/"+runtimeID+"/"+claim.Command.ID+"/result", result, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalReviewAcrossHTTPDatabaseAndRuntimeProcesses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paged bool
		index bool
	}{{"legacy", false, false}, {"paged", true, false}, {"index", false, true}} {
		t.Run(tc.name, func(t *testing.T) { runLocalReviewProcessScenario(t, tc.paged, tc.index) })
	}
}

func runLocalReviewProcessScenario(t *testing.T, paged, index bool) {
	url := os.Getenv("LOCAL_REVIEW_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("LOCAL_REVIEW_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	fx := testutil.New(pool, "", "")
	fx.UserID = fx.Insert(t, "user", testutil.Cols{"name": "MR Test", "email": uuid.NewString() + "@example.test"})
	fx.WorkspaceID = fx.Insert(t, "workspace", testutil.Cols{"name": "MR Test", "slug": "mr-" + uuid.NewString(), "issue_prefix": "MR"})
	fx.Insert(t, "member", testutil.Cols{"workspace_id": fx.WorkspaceID, "user_id": fx.UserID, "role": "owner"})
	runtimeID := fx.Runtime(t, "remote-machine", testutil.Cols{"runtime_mode": "local", "provider": "codex"})
	agentID := fx.Agent(t, "review-agent", runtimeID)
	taskID := uuid.NewString()
	workspacesRoot := t.TempDir()
	key := strings.ReplaceAll(taskID, "-", "")
	envRoot := filepath.Join(workspacesRoot, fx.WorkspaceID, key[len(key)-12:])
	if err := os.MkdirAll(envRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, err := json.Marshal(execenv.EnvRootOwner{WorkspaceID: fx.WorkspaceID, TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envRoot, ".task_owner"), owner, 0o600); err != nil {
		t.Fatal(err)
	}
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review Test")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	checkout := filepath.Join(envRoot, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", checkout)
	contents := "delivered from runtime\n"
	if paged {
		contents = strings.Repeat(contents, 400000)
	}
	if err := os.WriteFile(filepath.Join(checkout, "app.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, checkout, "add", "app.txt")
	worktreeTestGit(t, checkout, "commit", "-m", "feature")
	sourceHead := worktreeTestGit(t, checkout, "rev-parse", "HEAD")
	fx.Task(t, agentID, testutil.Cols{"id": taskID, "runtime_id": runtimeID, "status": "completed", "work_dir": checkout})
	queries := db.New(pool)
	t.Cleanup(func() {
		if err := queries.DeleteWorkspacePullRequests(ctx, util.MustParseUUID(fx.WorkspaceID)); err != nil {
			t.Error(err)
		}
	})
	h := handler.New(queries, pool, nil, nil, nil, nil, nil, nil, handler.Config{})
	t.Setenv("JWT_SECRET", "isolated-local-mr-integration-signing-key")
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": fx.UserID, "email": "mr-owner@example.test", "exp": time.Now().Add(5 * time.Minute).Unix(),
	}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	// Use production JWT verification and membership gates, not identity stamps.
	router.Use(middleware.Auth(queries, nil, nil))
	router.With(middleware.RequireWorkspaceMember(queries), handler.RequireHumanActor).Post("/reviews", h.ForwardLocalReview)
	router.Post("/runtime/{runtimeId}/claim", h.ClaimLocalReviewRelay)
	router.Post("/runtime/{runtimeId}/{commandId}/result", h.ReportLocalReviewRelay)
	var statusChecks atomic.Int64
	router.Post("/api/daemon/runtimes/{runtimeId}/local-reviews/relay/{commandId}/status", func(w http.ResponseWriter, r *http.Request) {
		statusChecks.Add(1)
		h.LocalReviewRelayStatus(w, r)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	wrongToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": fx.UserID}).SignedString([]byte("wrong-fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, bearer, cookie string
		status               int
	}{
		{"spoofed identity without auth", "", "", http.StatusUnauthorized},
		{"wrong JWT signature", wrongToken, "", http.StatusUnauthorized},
		{"cookie write without CSRF", "", token, http.StatusForbidden},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/reviews", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-User-ID", fx.UserID)
		req.Header.Set("X-Workspace-ID", fx.WorkspaceID)
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "multica_auth", Value: tc.cookie})
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s: status=%d want=%d", tc.name, response.StatusCode, tc.status)
		}
	}
	call := func(method, path string, input any) map[string]json.RawMessage {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", fx.WorkspaceID)
		req.Header.Set("X-User-ID", "spoofed-client-identity")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, body)
		}
		var result map[string]json.RawMessage
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	runWorker := func(input protocol.LocalReviewCommand) map[string]json.RawMessage {
		t.Helper()
		workerCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		command := exec.CommandContext(workerCtx, os.Args[0], "-test.run=^TestLocalReviewRuntimeProcessHelper$", "-test.count=1")
		command.Env = append(os.Environ(), "MULTICA_REVIEW_SUBPROCESS=1", "MR_TEST_ROOT="+workspacesRoot, "MR_TEST_SERVER="+server.URL, "MR_TEST_TOKEN="+token, "MR_TEST_RUNTIME="+runtimeID)
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if t.Failed() {
				cancel()
			}
			if err := command.Wait(); err != nil {
				t.Errorf("runtime process: %v: %s", err, output.String())
			}
		}()
		return call("POST", "/reviews", input)
	}
	fixture := processReviewFixture{taskID: taskID, checkout: checkout, repo: repo, sourceHead: sourceHead, run: runWorker}
	if index {
		verifyIndexReviewProcess(t, fixture)
	} else if paged {
		verifyPagedReviewProcess(t, fixture)
	} else {
		verifyLegacyReviewProcess(t, fixture)
	}
	var hasCloudMRStorage bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('local_review') IS NOT NULL
		OR to_regclass('local_review_event') IS NOT NULL
		OR to_regclass('local_review_command') IS NOT NULL`).Scan(&hasCloudMRStorage); err != nil {
		t.Fatal(err)
	}
	if hasCloudMRStorage {
		t.Fatal("fresh installation created cloud MR storage")
	}
	if paged && statusChecks.Load() == 0 {
		t.Fatal("paged runtime never checked the authenticated reader status")
	}
}
