package daemon

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceFileAdmissionCapabilityRequiresManagedService(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, recursive := range []bool{false, true} {
			t.Run(fmt.Sprintf("managed=%t/recursive=%t", managed, recursive), func(t *testing.T) {
				d := worktreeTestDaemon(t)
				d.environmentServiceOwner = managed
				d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
				root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)

				result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{ID: "list", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", Path: root, Action: "workspace_files", Recursive: &recursive, Limit: 10})

				var page struct {
					IdentitySupported bool `json:"file_operation_identity_supported"`
				}
				if err := json.Unmarshal(result.Page, &page); err != nil || result.Error != "" {
					t.Fatalf("directory unavailable: %s, decode: %v", result.Error, err)
				}
				if page.IdentitySupported != managed {
					t.Fatalf("admission capability %t, want %t", page.IdentitySupported, managed)
				}
			})
		}
	}
}
