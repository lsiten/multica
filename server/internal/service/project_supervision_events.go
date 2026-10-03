package service

import (
	"context"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Publish authoritative issue snapshots after the transaction commits so board
// filters and assignment projections reflect supervisor writes immediately.
func (s *ProjectSupervisionService) publishIssueChanges(ctx context.Context, snapshot ProjectSupervisionSnapshot, queued []db.AgentTaskQueue, before map[string]db.Issue) {
	if s.Tasks.Bus == nil {
		return
	}
	ids := map[string]bool{}
	previous := map[string]string{}
	for _, item := range snapshot.Issues {
		previous[item.ID] = item.Status
	}
	for id, issue := range before {
		ids[id] = true
		previous[id] = issue.Status
	}
	for _, task := range queued {
		if task.IssueID.Valid {
			ids[util.UUIDToString(task.IssueID)] = true
		}
	}
	for raw := range ids {
		id, err := util.ParseUUID(raw)
		if err != nil {
			continue
		}
		current, err := s.Tasks.Queries.GetIssue(ctx, id)
		if err != nil {
			slog.Warn("load supervised issue for publication", "issue_id", raw, "error", err)
			continue
		}
		old, changed := before[raw]
		assigned := changed && (old.AssigneeID != current.AssigneeID || old.AssigneeType != current.AssigneeType)
		s.Tasks.Bus.Publish(events.Event{Type: protocol.EventIssueUpdated, WorkspaceID: util.UUIDToString(current.WorkspaceID), ActorType: "system", Payload: map[string]any{
			"issue":          IssueToMapResolved(ctx, s.Tasks.Queries, current, s.Tasks.getIssuePrefix(current.WorkspaceID)),
			"status_changed": previous[raw] != current.Status, "prev_status": previous[raw],
			"assignee_changed": assigned, "prev_assignee_id": util.UUIDToPtr(old.AssigneeID), "prev_assignee_type": old.AssigneeType.String,
		}})
	}
}
