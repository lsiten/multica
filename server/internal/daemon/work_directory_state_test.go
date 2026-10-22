package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkDirectoryReconciliationReportsRemovedAndRestoredDirectories(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "removed")
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	reports := make(chan []protocol.WorkDirectoryState, 2)
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/daemon/runtimes/runtime/work-directories" {
			t.Errorf("unexpected state report: %s %s", r.Method, r.URL.Path)
		}
		var states []protocol.WorkDirectoryState
		if err := json.NewDecoder(r.Body).Decode(&states); err != nil {
			t.Error(err)
		}
		reports <- states
		w.WriteHeader(http.StatusNoContent)
	}))
	paths := []string{removed, root, file, filepath.Join(file, "removed")}
	if err := d.reportWorkDirectoryStates(t.Context(), "runtime", paths); err != nil {
		t.Fatal(err)
	}
	states := <-reports
	if len(states) != 4 || !states[0].Missing || states[1].Missing || !states[2].Missing || !states[3].Missing {
		t.Fatalf("wrong filesystem states: %+v", states)
	}
	if err := os.Mkdir(removed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.reportWorkDirectoryStates(t.Context(), "runtime", []string{removed}); err != nil {
		t.Fatal(err)
	}
	if states = <-reports; len(states) != 1 || states[0].Missing {
		t.Fatalf("restored directory was not reported: %+v", states)
	}
}

func TestWorkDirectoryReconciliationRejectsInvalidPages(t *testing.T) {
	for _, paths := range [][]string{{"relative"}, {"/before"}, {"/z", "/a"}} {
		t.Run(paths[0], func(t *testing.T) {
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("after") != "/cursor" {
					t.Error("missing reconciliation cursor")
				}
				if err := json.NewEncoder(w).Encode(protocol.RuntimeWorkDirectoryPage{Paths: paths}); err != nil {
					t.Error(err)
				}
			}))
			if _, err := d.client.listRuntimeWorkDirectories(t.Context(), "runtime", "/cursor"); err == nil {
				t.Fatal("invalid filesystem paths were accepted")
			}
		})
	}
}

func TestWorkDirectoryReconciliationPreservesUnknownPathsWithoutBlockingOtherReports(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	reports := make(chan []protocol.WorkDirectoryState, 1)
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var states []protocol.WorkDirectoryState
		if err := json.NewDecoder(r.Body).Decode(&states); err != nil {
			t.Error(err)
		}
		reports <- states
		w.WriteHeader(http.StatusNoContent)
	}))
	missing := filepath.Join(root, "removed")
	if err := d.reportWorkDirectoryStates(t.Context(), "runtime", []string{loop, missing}); err != nil {
		t.Fatal(err)
	}
	if states := <-reports; len(states) != 1 || states[0].Path != missing || !states[0].Missing {
		t.Fatalf("uncertain path changed state or blocked reconciliation: %+v", states)
	}
}

func TestWorkDirectoryStateLoopBackfillsHistoricalMissingPaths(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 501)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("previously-cleaned-%03d", i))
	}
	reports := make(chan []protocol.WorkDirectoryState, 2)
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			page := paths[:500]
			if after := r.URL.Query().Get("after"); after != "" {
				if after != paths[499] {
					t.Errorf("unexpected directory cursor: %s", after)
				}
				page = paths[500:]
			}
			if err := json.NewEncoder(w).Encode(protocol.RuntimeWorkDirectoryPage{Paths: page}); err != nil {
				t.Error(err)
			}
			return
		}
		var states []protocol.WorkDirectoryState
		if err := json.NewDecoder(r.Body).Decode(&states); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
		reports <- states
	}))
	d.workspaces["ws1"] = newWorkspaceState("ws1", []string{"runtime"}, "", nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.workDirectoryStateLoop(ctx) }()
	count := 0
	for count < len(paths) {
		select {
		case states := <-reports:
			for _, state := range states {
				if state.Path != paths[count] || !state.Missing {
					t.Fatalf("historical path %d was not reconciled: %+v", count, state)
				}
				count++
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("startup reconciled only %d of %d historical directories", count, len(paths))
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("directory reconciliation did not stop")
	}
}

func TestWorkDirectoryCleanupReportSurvivesOperationCancellation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "removed")
	reports := make(chan []protocol.WorkDirectoryState, 1)
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var states []protocol.WorkDirectoryState
		if err := json.NewDecoder(r.Body).Decode(&states); err != nil {
			t.Error(err)
		}
		reports <- states
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d.reportTaskWorkDirectoryState(ctx, &protocol.TaskGCStatus{RuntimeID: "runtime", WorkDir: missing})
	select {
	case states := <-reports:
		if len(states) != 1 || states[0].Path != missing || !states[0].Missing {
			t.Fatalf("removed directory state was not reported: %+v", states)
		}
	case <-time.After(time.Second):
		t.Fatal("completed cleanup lost its directory report with the cancelled operation")
	}
}
