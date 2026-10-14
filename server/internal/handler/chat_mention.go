package handler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// notifyGroupChatMentions adds the human-facing side effects of @-mentions in a
// group chat message. It runs after the message has been persisted and the
// session's main agent has been enqueued (the send handler owns that), so it
// only produces the notifications a mention implies:
//
//   - @all    notifies every human participant, except the sender.
//   - @member notifies that member, but only when the member is a participant
//     of the group. An @-mention of someone who is not in the room is a no-op —
//     it never reaches a member who is not in the group.
//
// The session's main agent runs on every send regardless of mentions, so an
// @agent mention that names the main agent needs no action here; the run
// already happened. A @-mention of a member is a notification, never an agent
// run.
//
// Best-effort: the message and the agent run have already committed, so a
// notification failure is logged and must not fail the send.
func (h *Handler) notifyGroupChatMentions(ctx context.Context, session db.ChatSession, senderID pgtype.UUID, content, messageID string) {
	if session.Kind != "group" {
		return
	}
	mentions := util.ParseMentions(content)
	if len(mentions) == 0 {
		return
	}

	// The human participants of the group, keyed by member id. Only these may
	// be notified: an @-mention of someone who is not in the room is a no-op.
	participants, err := h.Queries.ListGroupParticipants(ctx, session.ID)
	if err != nil {
		slog.Warn("group chat mentions: failed to load participants", "session_id", util.UUIDToString(session.ID), "error", err)
		return
	}
	humanParticipants := make(map[pgtype.UUID]struct{}, len(participants))
	for _, p := range participants {
		if p.MemberType == "member" {
			humanParticipants[p.MemberID] = struct{}{}
		}
	}

	// Resolve who to notify: dedup by member id, and never notify the sender.
	toNotify := make(map[pgtype.UUID]struct{})
	for _, m := range mentions {
		if m.IsMentionAll() {
			for id := range humanParticipants {
				if id != senderID {
					toNotify[id] = struct{}{}
				}
			}
			continue
		}
		if m.Type != "member" {
			continue
		}
		id, err := util.ParseUUID(m.ID)
		if err != nil {
			continue
		}
		if _, ok := humanParticipants[id]; ok && id != senderID {
			toNotify[id] = struct{}{}
		}
	}
	if len(toNotify) == 0 {
		return
	}
	for id := range toNotify {
		h.createChatMentionInbox(ctx, session, id, senderID, content, messageID)
	}
}

// createChatMentionInbox writes one inbox item for a @-mentioned group member
// and broadcasts it. Best-effort: a failure is logged, never returned.
func (h *Handler) createChatMentionInbox(ctx context.Context, session db.ChatSession, recipient, sender pgtype.UUID, content, messageID string) {
	details, _ := json.Marshal(map[string]any{
		"chat_session_id": util.UUIDToString(session.ID),
		"message_id":      messageID,
	})
	item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   session.WorkspaceID,
		RecipientType: "member",
		RecipientID:   recipient,
		Type:          "chat_mentioned",
		Severity:      "info",
		IssueID:       pgtype.UUID{},
		Title:         session.Title,
		Body:          pgtype.Text{String: content, Valid: true},
		ActorType:     pgtype.Text{String: "member", Valid: true},
		ActorID:       sender,
		Details:       details,
	})
	if err != nil {
		slog.Warn("group chat mention: failed to create inbox item", "recipient", util.UUIDToString(recipient), "error", err)
		return
	}
	h.publish(protocol.EventInboxNew, util.UUIDToString(session.WorkspaceID), "member", util.UUIDToString(sender), map[string]any{
		"item": inboxToResponse(item),
	})
}
