package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestObsoleteArchiveCleanupRespectsProfileAndRestoreReservation(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
	}))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	writeLifecycleFile(t, filepath.Join(root, "output", "old-file"), "old backup")
	preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
	archived := d.archiveEnvironmentOperation(t.Context(), root, preview.Revision, strings.Repeat("a", 64))
	if !archived.Reclaimed {
		t.Fatalf("legacy archive fixture failed: %+v", archived)
	}
	archive := filepath.Join(d.cfg.WorkspacesRoot, ".environment-archive", archived.ArchiveID)
	release, ok := d.reserveEnvRootForGC(root)
	if !ok {
		t.Fatal("restore reservation unavailable")
	}
	d.cleanupObsoleteEnvironmentArchives(t.Context())
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("reserved restore source deleted: %v", err)
	}
	release()
	d.cfg.Profile = "another-profile"
	d.cleanupObsoleteEnvironmentArchives(t.Context())
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("foreign profile archive deleted: %v", err)
	}
	d.cfg.Profile = ""
	d.cleanupObsoleteEnvironmentArchives(t.Context())
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("obsolete archive was retained: %v", err)
	}
	if _, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, archived.ArchiveID); err == nil {
		t.Fatal("deleted archive remains readable")
	}
}
