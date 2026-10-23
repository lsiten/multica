package handler

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Markers only admit the selected managed checkout while its current family
// still needs it. The batch query resolves reparenting and open peers together.
func (h *Handler) chatFileCodeWorkspaceVisibility(ctx context.Context, workspaceID pgtype.UUID, groups ...[]db.AgentTaskQueue) (map[pgtype.UUID]bool, error) {
	visible := make(map[pgtype.UUID]bool)
	selections := make(map[pgtype.UUID]protocol.CodeWorkspaceSelection)
	issues := make(map[pgtype.UUID]pgtype.UUID)
	issueIDs := make([]pgtype.UUID, 0)
	seenIssues := make(map[pgtype.UUID]bool)
	for _, tasks := range groups {
		for _, task := range tasks {
			var result map[string]json.RawMessage
			if json.Unmarshal(task.Result, &result) != nil {
				continue
			}
			raw, present := result["code_workspace"]
			if !present {
				continue
			}
			visible[task.ID] = false
			var selection protocol.CodeWorkspaceSelection
			if task.ChatSessionID.Valid || !task.IssueID.Valid || !task.RuntimeID.Valid || json.Unmarshal(raw, &selection) != nil || selection.WorkDir == "" || selection.WorkDir != task.WorkDir.String || selection.RuntimeID != uuidToString(task.RuntimeID) {
				continue
			}
			selections[task.ID] = selection
			issues[task.ID] = task.IssueID
			if !seenIssues[task.IssueID] {
				issueIDs = append(issueIDs, task.IssueID)
				seenIssues[task.IssueID] = true
			}
		}
	}
	if len(issueIDs) == 0 {
		return visible, nil
	}
	facts, err := h.Queries.ListCodeWorkspaceRetentions(ctx, db.ListCodeWorkspaceRetentionsParams{WorkspaceID: workspaceID, IssueIds: issueIDs})
	if err != nil {
		return nil, err
	}
	families := make(map[pgtype.UUID]db.ListCodeWorkspaceRetentionsRow, len(facts))
	for _, fact := range facts {
		families[fact.IssueID] = fact
	}
	for taskID, selection := range selections {
		fact, found := families[issues[taskID]]
		visible[taskID] = found && fact.RetentionBlocked && selection.IssueID == uuidToString(fact.RootIssueID)
	}
	return visible, nil
}
