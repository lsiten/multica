package handler

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTaskSupplementCompletionDoesNotReplayBoundComments(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "grok", "cursor", "kiro", "pi", "antigravity", "hermes"} {
		for _, status := range []string{"pending", "delivering", "delivered", "failed"} {
			for _, ordinary := range []string{"none", "unhandled", "queued"} {
				t.Run(provider+"/"+status+"/"+ordinary, func(t *testing.T) {
					fixture := newSupplementFixture(t, provider, "running", true)
					// A plain reply routes back to the agent owning the original thread.
					dbfx.Exec(t, `UPDATE comment SET content = $2 WHERE id = $1`, fixture.triggerID,
						fmt.Sprintf("[@Supplement](mention://agent/%s) original objective", fixture.agentID))
					dbfx.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids = ARRAY[trigger_comment_id] WHERE id = $1`, fixture.taskID)
					dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id = $1`, fixture.issueID)

					var ordinaryID, queuedID string
					if ordinary != "none" {
						ordinaryID = dbfx.Comment(t, fixture.issueID, "Handle this ordinary reply next.", testutil.Cols{
							"parent_id": fixture.triggerID,
						})
					}
					if ordinary == "queued" {
						queuedID = dbfx.Task(t, fixture.agentID, testutil.Cols{
							"issue_id": fixture.issueID, "runtime_id": fixture.runtimeID,
							"trigger_comment_id": ordinaryID,
						})
					}

					var supplement CommentResponse
					supplementRequest(t, fixture, "0199a4e8-22ce-7b01-bba5-999999999999", "Add this only to the current run.").
						Want(http.StatusCreated).JSON(&supplement)
					// Receipt transitions are covered by the retry test; vary only the
					// persisted state consumed by completion reconciliation here.
					dbfx.Exec(t, `UPDATE task_supplement SET status = $2 WHERE comment_id = $1`, supplement.ID, status)

					// Drive the real completion handler: a direct SQL status update skips
					// the reconciliation that previously replayed the supplement.
					req := newOwnerDaemonRequest(http.MethodPost, "/api/daemon/tasks/"+fixture.taskID+"/complete",
						map[string]any{"output": "done"})
					testutil.Call(t, testHandler.CompleteTask, withURLParam(req, "taskId", fixture.taskID)).Want(http.StatusOK)
					if status == "pending" || status == "delivering" {
						receipt, err := testHandler.Queries.GetTaskSupplementForRun(t.Context(), db.GetTaskSupplementForRunParams{
							CommentID: parseUUID(supplement.ID), TaskID: parseUUID(fixture.taskID), WorkspaceID: parseUUID(testWorkspaceID),
						})
						if err != nil || receipt.Status != "failed" || receipt.FailureReason.String != protocol.TaskSupplementFailureTurnEnded {
							t.Fatalf("completion did not settle pending delivery: %#v: %v", receipt, err)
						}
					}

					wantTasks := 1
					if ordinary != "none" {
						wantTasks++
					}
					if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, fixture.issueID); n != wantTasks {
						var diagnostic []byte
						diagnosticErr := testPool.QueryRow(t.Context(), `
							SELECT json_build_object(
								'task_created_at', t.created_at, 'comment_created_at', c.created_at,
								'task_thread_id', t.comment_thread_id, 'comment_root_id', comment_thread_root_id(c.id),
								'comment_after_task', c.created_at > t.created_at,
								'already_delivered', c.id = ANY(t.delivered_comment_ids))
							FROM agent_task_queue t
							LEFT JOIN comment c ON c.id = NULLIF($2, '')::uuid
							WHERE t.id = $1
						`, fixture.taskID, ordinaryID).Scan(&diagnostic)
						t.Fatalf("completion left %d runs, want %d; supplement must never create a follow-up: task=%s ordinary=%s diagnostic=%s error=%v", n, wantTasks, fixture.taskID, ordinaryID, diagnostic, diagnosticErr)
					}
					if ordinary != "none" {
						var followupID, triggerID string
						var coalesced []string
						dbfx.QueryRow(t, `SELECT id, trigger_comment_id, coalesced_comment_ids::text[]
						FROM agent_task_queue WHERE issue_id = $1 AND id <> $2 AND status = 'queued'`,
							fixture.issueID, fixture.taskID).Scan(&followupID, &triggerID, &coalesced)
						if queuedID != "" && followupID != queuedID {
							t.Fatalf("replaced existing queued run %s with %s", queuedID, followupID)
						}
						if triggerID != ordinaryID || slices.Contains(coalesced, supplement.ID) {
							t.Fatalf("follow-up input = trigger %s, coalesced %v; want ordinary comment %s without supplement %s",
								triggerID, coalesced, ordinaryID, supplement.ID)
						}
					}
				})
			}
		}
	}
}
