package daemon

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// Keep both pending reservations and completed receipts indefinitely. Task
// replay has no maximum age; deleting either can repeat an external action.
// This daemon-owned dot directory is excluded from execution-environment GC.
func communicationReceiptPath(root string, task Task, channel string) string {
	workspace := fmt.Sprintf("%x", sha256.Sum256([]byte(task.WorkspaceID)))
	taskKey := fmt.Sprintf("%x", sha256.Sum256([]byte(task.ID)))
	return filepath.Join(root, ".communication-receipts", workspace, taskKey, channel)
}
