package handler

import (
	"path"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Reuse must be proven from persisted task metadata, never from a client-supplied
// owner ID or a directory-name suffix. Empty task/chat scopes fail closed.
func reviewTasksShareDirectory(current, owner db.AgentTaskQueue, selected string) bool {
	if !current.RuntimeID.Valid || current.RuntimeID != owner.RuntimeID || !current.AgentID.Valid || current.AgentID != owner.AgentID {
		return false
	}
	sameIssue := current.IssueID.Valid && current.IssueID == owner.IssueID
	sameChat := current.ChatSessionID.Valid && current.ChatSessionID == owner.ChatSessionID
	if (!sameIssue && !sameChat) || current.IssueID != owner.IssueID || current.ChatSessionID != owner.ChatSessionID {
		return false
	}
	if !current.WorkDir.Valid || !owner.WorkDir.Valid || current.WorkDir.String == "" || current.WorkDir.String != owner.WorkDir.String || selected == "" {
		return false
	}
	base := path.Clean(strings.ReplaceAll(current.WorkDir.String, "\\", "/"))
	requested := path.Clean(strings.ReplaceAll(selected, "\\", "/"))
	return requested == base || strings.HasPrefix(requested, strings.TrimRight(base, "/")+"/")
}
