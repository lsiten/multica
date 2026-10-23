package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSelectedCodeWorkspaceRequiresManagedFamilyProvenance(t *testing.T) {
	for _, name := range []string{"canonical", "foreign root", "foreign runtime", "foreign project", "foreign repository", "private checkout", "private provenance", "external", "missing provenance", "chat", "no family"} {
		t.Run(name, func(t *testing.T) {
			d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir()}}
			task := Task{ID: "child-run", IssueID: "child", CodeWorkspaceIssueID: "root", WorkspaceID: "workspace", RuntimeID: "runtime", ProjectID: "project"}
			root := filepath.Join(d.cfg.WorkspacesRoot, "workspace", "first-run")
			if name == "external" {
				root = t.TempDir()
			}
			workdir := filepath.Join(root, "workdir")
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				t.Fatal(err)
			}
			scope, err := managedScopeForTask(task)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "foreign root":
				scope.CodeWorkspaceIssueID = "other"
			case "foreign runtime":
				scope.RuntimeID = "other"
			case "foreign project":
				scope.ProjectID = "other"
			case "foreign repository":
				scope.RepositoryScope = "other"
			case "private provenance":
				scope.PrivateProviderCheckout = true
			case "chat":
				task.ChatSessionID = "chat"
			case "no family":
				task.CodeWorkspaceIssueID = ""
			}
			if name != "missing provenance" {
				if err := execenv.WriteManagedEnvProvenance(root, scope); err != nil {
					t.Fatal(err)
				}
			}
			env := &execenv.Environment{WorkDir: workdir, RootDir: root, PrivateProviderCheckout: name == "private checkout"}
			selected := d.selectedCodeWorkspace(task, env)
			if name != "canonical" {
				if selected != nil {
					t.Fatalf("unproven selection: %+v", selected)
				}
				return
			}
			want := protocol.CodeWorkspaceSelection{IssueID: "root", RuntimeID: "runtime", WorkDir: workdir}
			if selected == nil || *selected != want {
				t.Fatalf("selected=%+v want=%+v", selected, want)
			}
		})
	}
}

func TestStartTaskReportsActualCodeWorkspaceSelection(t *testing.T) {
	task := startTestClaim()
	task.SelectedCodeWorkspace = &protocol.CodeWorkspaceSelection{IssueID: "root", RuntimeID: task.RuntimeID, WorkDir: "/managed/workdir"}
	client := NewClient("https://daemon.test")
	client.client.Transport = startTaskTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			CodeWorkspace *protocol.CodeWorkspaceSelection `json:"code_workspace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.CodeWorkspace == nil || *body.CodeWorkspace != *task.SelectedCodeWorkspace {
			t.Fatalf("request selection: %+v", body.CodeWorkspace)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	if _, err := client.StartTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "code_workspace\"") {
		t.Fatal("local selection leaked into serialized claim")
	}
}

func TestRunTaskReportsFirstChildCanonicalWorkspace(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	task := leaderReuseTestTask("first-child")
	task.CodeWorkspaceIssueID = "root-that-never-ran"
	task.CodeWorkspaceIssueIdentifier = "ROOT-1"
	task.StartClaimSupported = true
	task.DispatchedAt = "2026-10-23T09:00:00Z"
	selected := make(chan protocol.CodeWorkspaceSelection, 1)
	d.client.client.Transport = startTaskTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			var body struct {
				CodeWorkspace *protocol.CodeWorkspaceSelection `json:"code_workspace"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			if body.CodeWorkspace != nil {
				selected <- *body.CodeWorkspace
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	result, err := d.runTask(context.Background(), task, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case proof := <-selected:
		if proof.IssueID != task.CodeWorkspaceIssueID || proof.RuntimeID != task.RuntimeID || proof.WorkDir != result.WorkDir {
			t.Fatalf("selected proof=%+v actual workdir=%s", proof, result.WorkDir)
		}
	default:
		t.Fatal("actual canonical workspace was not reported before start")
	}
}
