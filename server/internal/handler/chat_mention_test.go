package handler

import (
	"context"
	"testing"


	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// createTestMember inserts a second workspace member and returns its id. The
// group-mention path only needs the member to be a valid participant UUID, so
// a real workspace member is the faithful fixture.
func createTestMember(t *testing.T, email string) string {
	t.Helper()
	var userID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		email, email).Scan(&userID); err != nil {
		t.Fatalf("insert member user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, userID); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE user_id = $1`, userID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

// addGroupMemberParticipant inserts a member participant row for a group chat.
func addGroupMemberParticipant(t *testing.T, sessionID, memberID string) {
	t.Helper()
	if _, err := testHandler.Queries.AddGroupParticipant(context.Background(), db.AddGroupParticipantParams{
		ChatSessionID: parseUUID(sessionID),
		MemberType:    "member",
		MemberID:      parseUUID(memberID),
	}); err != nil {
		t.Fatalf("add group member participant: %v", err)
	}
}

func countChatMentionInbox(t *testing.T, recipient string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_item WHERE recipient_id = $1 AND type = 'chat_mentioned'`,
		recipient).Scan(&n); err != nil {
		t.Fatalf("count inbox items: %v", err)
	}
	return n
}

// createTestGroupSession creates a standalone group chat whose session agent is
// the given agent; the test user is added as the creator (a member
// participant). Returns the session id.
func createTestGroupSession(t *testing.T, agentID string) string {
	t.Helper()
	req := withChatTestWorkspaceCtx(t, newRequest("POST", "/api/chat/sessions/groups", map[string]any{
		"agent_id": agentID,
		"title":    "Mention group",
	}))
	var out ChatSessionResponse
	testutil.Call(t, testHandler.CreateGroupChat, req).Want(201).JSON(&out)
	return out.ID
}

func TestNotifyGroupChatMentions(t *testing.T) {
	agentID := createHandlerTestAgent(t, "MentionGroupAgent", []byte("[]"))
	sessionID := createTestGroupSession(t, agentID)

	// The test user is already a member participant (the creator). Add a second
	// member to the group so it can be @-mentioned.
	second := createTestMember(t, "mentioned-second@multica.ai")
	addGroupMemberParticipant(t, sessionID, second)

	session, err := testHandler.Queries.GetChatSession(context.Background(), parseUUID(sessionID))
	if err != nil {
		t.Fatalf("load session: %v", err)
	}

	t.Run("member mention + @all notifies the participant once and never the sender", func(t *testing.T) {
		content := "hello " + mentionMarkdown("member", second, "Second") + " " + mentionMarkdown("all", "all", "all")
		testHandler.notifyGroupChatMentions(context.Background(), session, parseUUID(testUserID), content, "msg-mention-1")

		if got := countChatMentionInbox(t, second); got != 1 {
			t.Errorf("mentioned member inbox items = %d, want 1 (dedup across @member + @all)", got)
		}
		if got := countChatMentionInbox(t, testUserID); got != 0 {
			t.Errorf("sender inbox items = %d, want 0 (the sender must never notify itself)", got)
		}
	})

	t.Run("member mention only notifies the participant", func(t *testing.T) {
		content := "ping " + mentionMarkdown("member", second, "Second")
		testHandler.notifyGroupChatMentions(context.Background(), session, parseUUID(testUserID), content, "msg-mention-2")

		if got := countChatMentionInbox(t, second); got != 2 {
			t.Errorf("mentioned member inbox items = %d, want 2 (two messages mention them)", got)
		}
	})

	t.Run("mention of a non-participant is a no-op", func(t *testing.T) {
		stranger := createTestMember(t, "stranger@multica.ai")
		// NOT added as a participant.
		content := "hey " + mentionMarkdown("member", stranger, "Stranger")
		testHandler.notifyGroupChatMentions(context.Background(), session, parseUUID(testUserID), content, "msg-mention-3")

		if got := countChatMentionInbox(t, stranger); got != 0 {
			t.Errorf("non-participant inbox items = %d, want 0 (only group participants may be mentioned)", got)
		}
	})

	t.Run("no mention creates no notification", func(t *testing.T) {
		testHandler.notifyGroupChatMentions(context.Background(), session, parseUUID(testUserID), "just a plain message", "msg-plain")
		if got := countChatMentionInbox(t, second); got != 2 {
			t.Errorf("mentioned member inbox items = %d, want 2 (plain message added none)", got)
		}
	})

	t.Run("non-group session is a no-op", func(t *testing.T) {
		oneToOne, err := testHandler.Queries.GetChatSession(context.Background(), parseUUID(sessionID))
		if err != nil {
			t.Fatalf("load 1:1 session: %v", err)
		}
		oneToOne.Kind = "chat" // not a group
		content := "hey " + mentionMarkdown("member", second, "Second") + " " + mentionMarkdown("all", "all", "all")
		testHandler.notifyGroupChatMentions(context.Background(), oneToOne, parseUUID(testUserID), content, "msg-1to1")
		// A non-group session never notifies, even with @all / @member present.
		// (The second member's count is unchanged from the plain-message case.)
		if got := countChatMentionInbox(t, second); got != 2 {
			t.Errorf("non-group inbox items = %d, want 2 (non-group sessions do not process mentions)", got)
		}
	})
}

// mentionMarkdown builds a mention link the parser accepts.
func mentionMarkdown(mentionType, id, label string) string {
	return "[" + label + "](mention://" + mentionType + "/" + id + ")"
}

