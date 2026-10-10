package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEnvironmentGitAuthRequiresFreshRepositoryBinding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "owned-git-marker")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	t.Setenv("MULTICA_TOKEN", "private-account-marker")
	t.Setenv("OPENAI_API_KEY", "private-provider-marker")
	var allowed atomic.Bool
	allowed.Store(true)
	var reads atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/daemon/workspaces/workspace/repos" || r.Header.Get("Authorization") != "Bearer parent-marker" {
			http.Error(w, "unauthorized", 403)
			return
		}
		reads.Add(1)
		result := WorkspaceReposResponse{WorkspaceID: "workspace"}
		if allowed.Load() {
			result.Repos = []RepoData{{URL: "https://owned.invalid/repository"}}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer backend.Close()
	parent := NewClient(backend.URL)
	parent.token = "parent-marker"
	d := &Daemon{client: parent, workspaces: map[string]*workspaceState{"workspace": {allowedRepoURLs: map[string]struct{}{"https://owned.invalid/repository": {}}}}}
	callback, err := newEnvironmentFactCallback(d, "owned-instance", "owned-callback")
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	auth := environmentRepositoryAuth{facts: &environmentFactTransport{address: callback.address, instanceID: "owned-instance", token: "owned-callback", http: &http.Client{Transport: transport}}}
	environment, err := auth.GitEnvironment(context.Background(), "workspace", "HTTPS://OWNED.INVALID/repository/")
	if err != nil {
		t.Fatal(err)
	}
	if environment["GH_TOKEN"] != "owned-git-marker" {
		t.Fatal("authorized Git helper credential absent")
	}
	for key := range environment {
		if key != "GH_TOKEN" && key != "GITHUB_TOKEN" && key != "GH_ENTERPRISE_TOKEN" && key != "GITHUB_ENTERPRISE_TOKEN" {
			t.Fatalf("non-Git credential crossed boundary: key=%s", key)
		}
	}
	clear(environment)
	if _, err = auth.GitEnvironment(context.Background(), "workspace", "https://foreign.invalid/repository"); err == nil {
		t.Fatal("foreign repository received credentials")
	}
	if _, err = auth.GitEnvironment(context.Background(), "foreign-workspace", "https://owned.invalid/repository"); err == nil {
		t.Fatal("foreign workspace received credentials")
	}
	allowed.Store(false)
	if _, err = auth.GitEnvironment(context.Background(), "workspace", "https://owned.invalid/repository"); err == nil {
		t.Fatal("stale cached binding issued credentials after removal")
	}
	if reads.Load() != 2 {
		t.Fatalf("fresh authorization reads=%d want2", reads.Load())
	}
}
