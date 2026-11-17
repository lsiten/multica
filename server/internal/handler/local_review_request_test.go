package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestLocalReviewForwardRequiresOperationID(t *testing.T) {
	for _, action := range []string{"submit", "approve", "request_changes", "merge"} {
		t.Run(action, func(t *testing.T) {
			request := newRequestAsUser("11111111-1111-4111-8111-111111111111", http.MethodPost, "/api/local-reviews/execute", map[string]string{
				"task_id": "22222222-2222-4222-8222-222222222222", "target": "main", "action": action, "snapshot_id": "snapshot",
			})
			var response struct {
				Error string `json:"error"`
			}
			testutil.Call(t, (&Handler{}).ForwardLocalReview, request).Want(http.StatusBadRequest).JSON(&response)
			if response.Error != "valid operation ID required" {
				t.Fatalf("rejected for unrelated reason: %+v", response)
			}
		})
	}
}

func TestLocalReviewForwardRejectsMalformedDirectoryMode(t *testing.T) {
	request := newRequestAsUser("11111111-1111-4111-8111-111111111111", http.MethodPost, "/api/local-reviews/execute", map[string]any{"project_resource_id": "resource", "path": "/bound", "action": "workspace_files", "recursive": "false"})
	testutil.Call(t, (&Handler{}).ForwardLocalReview, request).Want(http.StatusBadRequest)
}

func TestLocalReviewForwardValidatesReviewScopeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "run needs task", body: map[string]any{"project_resource_id": "resource", "scope": "run", "path": "/bound", "action": "workspace_files"}, want: "run review scope requires a task"},
		{name: "directory needs resource", body: map[string]any{"task_id": "task", "scope": "directory", "path": "/bound", "action": "index"}, want: "directory review scope requires a project resource"},
		{name: "version needs version", body: map[string]any{"task_id": "task", "scope": "version", "path": "/repo", "target": "main", "action": "manifest"}, want: "version review scope requires a version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newRequestAsUser("11111111-1111-4111-8111-111111111111", http.MethodPost, "/api/local-reviews/execute", tc.body)
			var response struct {
				Error string `json:"error"`
			}
			testutil.Call(t, (&Handler{}).ForwardLocalReview, request).Want(http.StatusBadRequest).JSON(&response)
			if response.Error != tc.want {
				t.Fatalf("error = %q, want %q", response.Error, tc.want)
			}
		})
	}
}
