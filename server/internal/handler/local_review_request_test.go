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
