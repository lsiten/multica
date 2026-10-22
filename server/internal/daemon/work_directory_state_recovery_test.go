package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkDirectoryReconciliationRetriesUnacknowledgedPage(t *testing.T) {
	for _, failedMethod := range []string{http.MethodGet, http.MethodPost} {
		t.Run(failedMethod, func(t *testing.T) {
			root := t.TempDir()
			paths := make([]string, 501)
			for i := range paths {
				paths[i] = filepath.Join(root, fmt.Sprintf("removed-%03d", i))
			}
			var cursors []string
			var acknowledged []string
			failed := false
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					after := r.URL.Query().Get("after")
					cursors = append(cursors, after)
					if after != "" && failedMethod == http.MethodGet && !failed {
						failed = true
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					page := paths[:500]
					if after != "" {
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
				if len(states) == 1 && failedMethod == http.MethodPost && !failed {
					failed = true
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				for _, state := range states {
					acknowledged = append(acknowledged, state.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			after, err := d.reconcileRuntimeWorkDirectories(t.Context(), "runtime", "")
			if err == nil || after != paths[499] {
				t.Fatalf("lost acknowledged cursor after failure: after=%q, err=%v", after, err)
			}
			after, err = d.reconcileRuntimeWorkDirectories(t.Context(), "runtime", after)
			if err != nil || after != "" {
				t.Fatalf("failed to finish resumed sweep: after=%q, err=%v", after, err)
			}
			if !slices.Equal(acknowledged, paths) || !slices.Equal(cursors, []string{"", paths[499], paths[499]}) {
				t.Fatalf("recovery skipped or repeated acknowledged pages: paths=%d, cursors=%v", len(acknowledged), cursors)
			}
		})
	}
}
