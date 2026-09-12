package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type mirrorViewerDetails struct {
	RuntimeID   string `json:"runtime_id"`
	RuntimeName string `json:"runtime_name"`
	State       string `json:"state"`
}

// HandleDaemonMirrorViewer records the shared-source viewer transition and
// notifies the runtime owner on the first viewer and last viewer departure.
func (h *Handler) HandleDaemonMirrorViewer(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.MirrorViewerPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if identity.DaemonID == "" || payload.DaemonID != identity.DaemonID ||
		!identity.AllowsWorkspace(payload.WorkspaceID) ||
		(len(identity.RuntimeIDs) > 0 && !mirrorContainsString(identity.RuntimeIDs, payload.RuntimeID)) {
		return mirror.ErrSessionIdentityMismatch
	}

	runtimeUUID, err := util.ParseUUID(payload.RuntimeID)
	if err != nil {
		return err
	}
	workspaceUUID, err := util.ParseUUID(payload.WorkspaceID)
	if err != nil {
		return err
	}
	rt, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          runtimeUUID,
		WorkspaceID: workspaceUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load runtime for mirror viewer event: %w", err)
	}
	if !rt.DaemonID.Valid || rt.DaemonID.String != payload.DaemonID || !rt.OwnerID.Valid {
		return mirror.ErrSessionIdentityMismatch
	}

	if !payload.Active && strings.TrimSpace(payload.ViewerID) != "" && h.MirrorSessions != nil {
		if _, err := h.MirrorSessions.CloseViewer(ctx, payload.DaemonID, payload.RuntimeID, payload.ViewerID); err != nil {
			slog.Warn("close mirror signaling session after viewer departure failed",
				"runtime_id", payload.RuntimeID,
				"viewer_id", payload.ViewerID,
				"error", err,
			)
		}
	}
	changed := h.MirrorViewers.SetViewerActiveIf(payload.RuntimeID, payload.ViewerID, payload.Active, func() bool {
		return h.DaemonHub != nil && h.DaemonHub.RuntimeConnectionCount(payload.RuntimeID) > 0
	})
	if !changed {
		return nil
	}
	if err := h.createMirrorViewerNotice(ctx, rt, payload.Active); err != nil {
		slog.Warn("mirror viewer inbox write failed", "runtime_id", payload.RuntimeID, "active", payload.Active, "error", err)
		return err
	}
	return nil
}

// HandleDaemonMirrorDisconnect closes active viewer state only when no other
// authenticated connection still serves the runtime.
func (h *Handler) HandleDaemonMirrorDisconnect(ctx context.Context, identity daemonws.ClientIdentity) {
	if h.DaemonHub == nil || identity.DaemonID == "" {
		return
	}
	if h.MirrorSessions != nil {
		if _, err := h.MirrorSessions.CloseForDaemon(
			ctx,
			identity.DaemonID,
			identity.RuntimeIDs,
			func(runtimeID string) bool {
				return h.DaemonHub.RuntimeConnectionCount(runtimeID) == 0
			},
		); err != nil {
			slog.Warn("close mirror signaling sessions after daemon disconnect failed",
				"daemon_id", identity.DaemonID,
				"error", err,
			)
		}
	}
	if h.MirrorViewers != nil {
		activeRuntimeIDs := h.MirrorViewers.ResetIf(identity.RuntimeIDs, func(runtimeID string) bool {
			return h.DaemonHub.RuntimeConnectionCount(runtimeID) == 0
		})
		for _, runtimeID := range activeRuntimeIDs {
			runtimeUUID, err := util.ParseUUID(runtimeID)
			if err != nil {
				continue
			}
			rt, err := h.Queries.GetAgentRuntime(ctx, runtimeUUID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				slog.Warn("load runtime after mirror daemon disconnect failed", "runtime_id", runtimeID, "error", err)
				continue
			}
			if !rt.DaemonID.Valid || rt.DaemonID.String != identity.DaemonID || !rt.OwnerID.Valid {
				continue
			}
			if err := h.createMirrorViewerNotice(ctx, rt, false); err != nil {
				slog.Warn("mirror viewer disconnect inbox write failed", "runtime_id", runtimeID, "error", err)
			}
		}
	}
}

func (h *Handler) createMirrorViewerNotice(ctx context.Context, rt db.AgentRuntime, active bool) error {
	state := "ended"
	itemType := protocol.InboxTypeRuntimeMirrorViewerStopped
	title := "Screen mirroring ended"
	body := fmt.Sprintf("Screen mirroring for %s has ended.", mirrorRuntimeNoticeName(rt))
	if active {
		state = "started"
		itemType = protocol.InboxTypeRuntimeMirrorViewerStarted
		title = "Screen mirroring started"
		body = fmt.Sprintf("%s is being viewed.", mirrorRuntimeNoticeName(rt))
	}
	detailsJSON, err := json.Marshal(mirrorViewerDetails{
		RuntimeID:   util.UUIDToString(rt.ID),
		RuntimeName: mirrorRuntimeNoticeName(rt),
		State:       state,
	})
	if err != nil {
		return err
	}
	item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   rt.WorkspaceID,
		RecipientType: "member",
		RecipientID:   rt.OwnerID,
		Type:          itemType,
		Severity:      "info",
		IssueID:       pgtype.UUID{},
		Title:         title,
		Body:          pgtype.Text{String: body, Valid: true},
		ActorType:     pgtype.Text{String: "system", Valid: true},
		ActorID:       pgtype.UUID{},
		Details:       detailsJSON,
	})
	if err != nil {
		return fmt.Errorf("create mirror viewer inbox item: %w", err)
	}
	h.publish(protocol.EventInboxNew, util.UUIDToString(rt.WorkspaceID), "system", "", map[string]any{
		"item": inboxToResponse(item),
	})
	return nil
}

func mirrorRuntimeNoticeName(rt db.AgentRuntime) string {
	if customName := strings.TrimSpace(rt.CustomName.String); rt.CustomName.Valid && customName != "" {
		return customName
	}
	return rt.Name
}
