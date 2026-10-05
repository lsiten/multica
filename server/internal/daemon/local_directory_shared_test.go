package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestInPlaceTasksShareOccupiedDirectoryAndKeepGuardUntilLastRelease(t *testing.T) {
	for _, mode := range []string{"", "in_place"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			d.localPathLocks = NewLocalPathLocker()
			d.cfg.DaemonID = "shared-in-place"
			path, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ref, err := json.Marshal(localDirectoryRef{LocalPath: path, DaemonID: d.cfg.DaemonID, ExecutionMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			resources := []ProjectResourceData{{ID: "resource", ResourceType: localDirectoryResourceType, ResourceRef: ref}}
			oldHolder, err := d.localPathLocks.Acquire(t.Context(), path, "old-holder", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer oldHolder()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			first, abort := d.acquireLocalDirectoryLockIfNeeded(ctx, Task{ID: "issue-one", IssueID: "issue-one", ProjectResources: resources}, d.logger)
			if abort || first == nil || ctx.Err() != nil {
				t.Fatal("first issue run waited for the occupied directory")
			}
			defer first()
			second, abort := d.acquireLocalDirectoryLockIfNeeded(ctx, Task{ID: "issue-two", IssueID: "issue-two", ProjectResources: resources}, d.logger)
			if abort || second == nil || ctx.Err() != nil {
				t.Fatal("second issue run waited for the first")
			}
			defer second()
			marker := filepath.Join(path, execenv.TaskContextMarkerRelPath)
			first()
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("first run removed the second run's CLI guard: %v", err)
			}
			if release, ok := d.localPathLocks.guardReviewPaths([]string{path}); ok {
				release()
				t.Fatal("review merge ignored active shared writers")
			}
			second()
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("last run did not clean shared guard: %v", err)
			}
			cancel()
			if release, abort := d.acquireLocalDirectoryLockIfNeeded(ctx, Task{ID: "cancelled", ProjectResources: resources}, d.logger); !abort || release != nil {
				t.Fatal("cancelled run joined the shared directory")
			}
			if d.resourceWaitTasks.Load() != 0 {
				t.Fatal("shared execution was counted as waiting")
			}
		})
	}
}

func TestSharedDirectoryReviewGuardReleasesAfterAllWriters(t *testing.T) {
	locker := NewLocalPathLocker()
	path := t.TempDir()
	first, err := locker.TrackShared(t.Context(), path, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := locker.TrackShared(t.Context(), path, "second")
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	defer second()
	first()
	if release, ok := locker.guardReviewPaths([]string{filepath.Join(path, "child")}); ok {
		release()
		t.Fatal("review ignored the remaining writer")
	}
	second()
	release, ok := locker.guardReviewPaths([]string{path})
	if !ok {
		t.Fatal("review guard remained blocked after all writers finished")
	}
	release()
}
