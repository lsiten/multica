package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var ErrInvalidCodeWorkspaceSelection = errors.New("invalid code workspace selection")

type TaskStartOptions struct {
	EnableTaskSupplement bool
	CodeWorkspace        *protocol.CodeWorkspaceSelection
}

func recordCodeWorkspaceSelection(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, selection *protocol.CodeWorkspaceSelection) (db.AgentTaskQueue, error) {
	if selection == nil {
		return task, nil
	}
	runtimeID, runtimeErr := util.ParseUUID(selection.RuntimeID)
	rootID, rootErr := util.ParseUUID(selection.IssueID)
	if runtimeErr != nil || rootErr != nil || task.ChatSessionID.Valid || !task.IssueID.Valid || !task.RuntimeID.Valid || runtimeID != task.RuntimeID || strings.TrimSpace(selection.WorkDir) == "" || len(selection.WorkDir) > 4096 || strings.ContainsRune(selection.WorkDir, '\x00') {
		return task, ErrInvalidCodeWorkspaceSelection
	}
	issue, err := q.GetIssue(ctx, task.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return task, fmt.Errorf("%w: issue unavailable", ErrInvalidCodeWorkspaceSelection)
	}
	if err != nil {
		return task, fmt.Errorf("load selected workspace issue: %w", err)
	}
	agent, err := q.GetAgent(ctx, task.AgentID)
	if err != nil {
		return task, fmt.Errorf("load selected workspace agent: %w", err)
	}
	if issue.WorkspaceID != agent.WorkspaceID {
		return task, fmt.Errorf("%w: issue belongs to another workspace", ErrInvalidCodeWorkspaceSelection)
	}
	runtime, err := q.GetAgentRuntime(ctx, task.RuntimeID)
	if err != nil {
		return task, fmt.Errorf("load selected workspace runtime: %w", err)
	}
	if runtime.WorkspaceID != agent.WorkspaceID {
		return task, fmt.Errorf("%w: runtime belongs to another workspace", ErrInvalidCodeWorkspaceSelection)
	}
	root, err := ResolveCodeWorkspaceRoot(ctx, q, issue)
	if err != nil && !errors.Is(err, ErrInvalidCodeWorkspaceTree) {
		return task, err
	}
	if err != nil || rootID != root.ID {
		return task, fmt.Errorf("%w: issue family does not match", ErrInvalidCodeWorkspaceSelection)
	}
	validated := protocol.CodeWorkspaceSelection{IssueID: util.UUIDToString(root.ID), RuntimeID: util.UUIDToString(task.RuntimeID), WorkDir: selection.WorkDir}
	var previous struct {
		CodeWorkspace *protocol.CodeWorkspaceSelection `json:"code_workspace"`
	}
	if json.Unmarshal(task.Result, &previous) == nil && previous.CodeWorkspace != nil && *previous.CodeWorkspace != validated {
		return task, fmt.Errorf("%w: selection already recorded", ErrInvalidCodeWorkspaceSelection)
	}
	encoded, err := json.Marshal(validated)
	if err != nil {
		return task, fmt.Errorf("encode code workspace selection: %w", err)
	}
	return q.RecordCodeWorkspaceSelection(ctx, db.RecordCodeWorkspaceSelectionParams{ID: task.ID, WorkDir: validated.WorkDir, Selection: encoded})
}
