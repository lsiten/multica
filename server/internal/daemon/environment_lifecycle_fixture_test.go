package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Exercise the real point/batch shapes while keeping facts explicit in each test.
func taskLifecycleTestHandler(t *testing.T, facts func(string) protocol.TaskGCStatus) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tasks/gc-check") {
			var request struct {
				TaskIDs []string `json:"task_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			response := protocol.TaskGCBatch{Tasks: []protocol.TaskGCStatus{}}
			for _, id := range request.TaskIDs {
				status := facts(id)
				status.TaskID = id
				if strings.Contains(r.URL.Path, "/workspaces/"+status.WorkspaceID+"/runtimes/"+status.RuntimeID+"/") {
					response.Tasks = append(response.Tasks, status)
				}
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := parts[len(parts)-2]
		status := facts(id)
		status.TaskID = id
		if err := json.NewEncoder(w).Encode(status); err != nil {
			t.Error(err)
		}
	})
}
