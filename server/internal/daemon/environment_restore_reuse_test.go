package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLegacyArchiveIsNeverAutomaticallyRestoredForNewRun(t *testing.T) {
	for _, mode := range []string{"restore", "different_project", "corrupt", "scope_tampered"} {
		t.Run(mode, func(t *testing.T) {
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", WorkspaceID: "ws-leader", RuntimeID: "rt-leader", AgentID: "agent-leader", LifecycleSupported: true})
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-leader", "first", &execenv.GCMeta{WorkspaceID: "ws-leader", TaskID: "first"})
			workdir := filepath.Join(root, "workdir")
			writeLeaderTaskMarker(t, workdir, "agent-leader", "issue-leader")
			writeLeaderManagedEnvProvenance(t, workdir, "ws-leader", "issue-leader", "agent-leader")
			writeLifecycleFile(t, filepath.Join(workdir, "code.txt"), "restore changed code")
			preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
			archived := d.archiveEnvironmentOperation(t.Context(), root, preview.Revision, strings.Repeat("a", 64))
			if !archived.Reclaimed {
				t.Fatalf("archive failed: %+v", archived)
			}
			task := leaderReuseTestTask("second")
			task.PriorWorkDir = workdir
			if mode == "different_project" {
				task.ProjectID = "other"
			}
			if mode == "corrupt" {
				if err := os.WriteFile(filepath.Join(d.cfg.WorkspacesRoot, ".environment-archive", archived.ArchiveID, "environment.tar.gz"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "scope_tampered" {
				manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, archived.ArchiveID)
				if err != nil {
					t.Fatal(err)
				}
				manifest.ReuseScope.ProjectID = "other"
				data, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d.cfg.WorkspacesRoot, ".environment-archive", archived.ArchiveID, "manifest.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				task.ProjectID = "other"
			}
			claim, reused, _, ok, err := d.lockReusablePriorEnvRoot(t.Context(), task, nil, "")
			if claim != nil {
				claim.Release()
			}
			if err != nil || ok || reused != "" || claim != nil {
				t.Fatalf("legacy backup unexpectedly influenced fresh execution: %s %v %v", reused, ok, err)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("backup was materialized: %v", err)
			}

		})
	}
}
