package handler

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func agentAnswersRequest(taskID, path string) *http.Request {
	req := withURLParam(newRequest(http.MethodGet, path, nil), "taskId", taskID)
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{}))
}

func TestAgentAnswersPagesTextWithoutToolTraceOrLostUnicode(t *testing.T) {
	taskID := seedBatchTask(t, "agent-answers")
	long := strings.Repeat("汉字🙂", 3000)
	for i, entry := range []struct{ kind, content string }{
		{"thinking", "private reasoning"}, {"tool_use", "tool args"},
		{"text", long}, {"tool_result", "tool output"}, {"text", "final answer"},
	} {
		dbfx.Insert(t, "task_message", testutil.Cols{"task_id": taskID, "seq": i + 1, "type": entry.kind, "content": entry.content})
	}
	cursor, answer := "", ""
	for pages := 0; pages < 8; pages++ {
		path := "/api/tasks/" + taskID + "/answers?limit=1&cursor=" + url.QueryEscape(cursor)
		var page protocol.AgentAnswerPage
		response := testutil.Call(t, testHandler.ListAgentAnswers, agentAnswersRequest(taskID, path)).Want(http.StatusOK)
		response.JSON(&page)
		if len(response.Body.Bytes()) > 64<<10 {
			t.Fatal("answer page exceeded response byte limit")
		}
		if page.NextCursor == cursor && page.HasMore {
			t.Fatal("answer cursor did not advance")
		}
		for _, message := range page.Messages {
			if message.Type != "text" {
				t.Fatalf("tool trace leaked into answer read: %s", message.Type)
			}
			answer += message.Content
		}
		cursor = page.NextCursor
		if !page.HasMore {
			break
		}
	}
	if answer != long+"final answer" {
		t.Fatal("paged answer lost or duplicated Unicode content")
	}
}

func TestAgentAnswersRejectsInvalidCursorAndLimit(t *testing.T) {
	taskID := seedBatchTask(t, "agent-answer-boundary")
	dbfx.Insert(t, "task_message", testutil.Cols{"task_id": taskID, "seq": 1, "type": "text", "content": "x"})
	for _, query := range []string{"cursor=bad", "cursor=-1:0", "cursor=1:-1", "cursor=1:999999999999", "cursor=1:2147483647", "limit=0", "limit=51", "cursor=1:1"} {
		t.Run(query, func(t *testing.T) {
			path := "/api/tasks/" + taskID + "/answers?" + query
			testutil.Call(t, testHandler.ListAgentAnswers, agentAnswersRequest(taskID, path)).Want(http.StatusBadRequest)
		})
	}
}

func TestAgentAnswersBoundsEscapedPayload(t *testing.T) {
	taskID := seedBatchTask(t, "agent-answer-bytes")
	for i := range 30 {
		dbfx.Insert(t, "task_message", testutil.Cols{"task_id": taskID, "seq": i + 1, "type": "text", "content": strings.Repeat("\x01", 4096)})
	}
	var page protocol.AgentAnswerPage
	response := testutil.Call(t, testHandler.ListAgentAnswers, agentAnswersRequest(taskID, "/answers?limit=50")).Want(http.StatusOK)
	response.JSON(&page)
	if response.Body.Len() > 64<<10 || !page.HasMore || len(page.Messages) != 1 {
		t.Fatalf("escaped content is not bounded: bytes=%d messages=%d more=%v", response.Body.Len(), len(page.Messages), page.HasMore)
	}
}

func TestAgentAnswersPreservesChatOwnerBoundary(t *testing.T) {
	f := seedChatTaskPrivacy(t)
	for _, viewer := range []struct {
		userID string
		status int
	}{{f.ownerID, http.StatusOK}, {testUserID, http.StatusNotFound}} {
		req := withURLParam(newRequestAs(viewer.userID, http.MethodGet, "/answers", nil), "taskId", f.chatTaskID)
		req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{}))
		testutil.Call(t, testHandler.ListAgentAnswers, req).Want(viewer.status)
	}
}

func TestAgentAnswersReadsTerminalResultBeforeTranscriptDelivery(t *testing.T) {
	taskID := seedBatchTask(t, "agent-answer-terminal-result")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now(), result = $2::jsonb WHERE id = $1`, taskID, `{"output":"authoritative final result"}`)
	var page protocol.AgentAnswerPage
	testutil.Call(t, testHandler.ListAgentAnswers, agentAnswersRequest(taskID, "/api/tasks/"+taskID+"/answers?view=final")).Want(http.StatusOK).JSON(&page)
	if len(page.Messages) != 1 || page.Messages[0].Type != "final" || page.Messages[0].Content != "authoritative final result" || page.HasMore {
		t.Fatalf("terminal result was not readable before transcript delivery: %+v", page)
	}
}
