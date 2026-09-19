package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type mirrorControlDetails struct {
	RuntimeID        string `json:"runtime_id"`
	RuntimeName      string `json:"runtime_name"`
	State            string `json:"state"`
	ControllerUserID string `json:"controller_user_id,omitempty"`
	ViewerID         string `json:"viewer_id,omitempty"`
	SourceKind       string `json:"source_kind,omitempty"`
	SourceID         string `json:"source_id,omitempty"`
}

// HandleDaemonMirrorControlState tracks daemon-confirmed human input bindings,
// broadcasts the active controller metadata to workspace viewers, and notifies
// the runtime owner on zero-to-one and one-to-zero transitions. It never sees
// pointer, keyboard, text, or coordinate payloads.
func (h *Handler) HandleDaemonMirrorControlState(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.MirrorControlStatePayload) error {
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
	userUUID, err := util.ParseUUID(payload.UserID)
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
		return fmt.Errorf("load runtime for mirror control event: %w", err)
	}
	if !rt.DaemonID.Valid || rt.DaemonID.String != payload.DaemonID || !rt.OwnerID.Valid {
		return mirror.ErrSessionIdentityMismatch
	}
	if h.MirrorControlStates == nil {
		return nil
	}

	state := mirror.ControllerState{
		ViewerID: strings.TrimSpace(payload.ViewerID),
		UserID:   payload.UserID,
		Source:   payload.Source,
	}
	controllers, changed, presenceChanged := h.MirrorControlStates.SetState(payload.RuntimeID, state, payload.Active)
	if !changed {
		return nil
	}
	h.publishRuntimeMirrorControl(rt, controllers)
	if !presenceChanged {
		return nil
	}
	if err := h.createMirrorControlNotice(ctx, rt, userUUID, payload.Active, payload); err != nil {
		slog.Warn("mirror control inbox write failed",
			"runtime_id", payload.RuntimeID,
			"active", payload.Active,
			"error", err,
		)
		return err
	}
	return nil
}

func runtimeMirrorControlPayload(rt db.AgentRuntime, controllers mirror.ControllerStates) protocol.RuntimeMirrorControlPayload {
	payloadControllers := make([]protocol.RuntimeMirrorControlController, 0, len(controllers))
	for _, controller := range controllers {
		payloadControllers = append(payloadControllers, protocol.RuntimeMirrorControlController{
			ViewerID: controller.ViewerID,
			UserID:   controller.UserID,
			Source:   controller.Source,
		})
	}
	return protocol.RuntimeMirrorControlPayload{
		WorkspaceID: util.UUIDToString(rt.WorkspaceID),
		RuntimeID:   util.UUIDToString(rt.ID),
		Controllers: payloadControllers,
	}
}

func (h *Handler) publishRuntimeMirrorControl(rt db.AgentRuntime, controllers mirror.ControllerStates) {
	h.publish(protocol.EventRuntimeMirrorControl, util.UUIDToString(rt.WorkspaceID), "system", "", runtimeMirrorControlPayload(rt, controllers))
}

// GetMirrorControlState returns the current controller snapshot for one
// runtime. It carries identity metadata only and never includes input data.
func (h *Handler) GetMirrorControlState(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_control_state_read", runtimeID)
	if !ok {
		return
	}
	controllers := mirror.ControllerStates{}
	if h.MirrorControlStates != nil {
		controllers = h.MirrorControlStates.Snapshot(uuidToString(rt.ID))
	}
	writeJSON(w, http.StatusOK, runtimeMirrorControlPayload(rt, controllers))
}

func (h *Handler) resetMirrorControlStatesOnDisconnect(ctx context.Context, identity daemonws.ClientIdentity, runtimeIDs []string) {
	if h.MirrorControlStates == nil || len(runtimeIDs) == 0 {
		return
	}
	prior := h.MirrorControlStates.ResetIf(runtimeIDs, nil)
	for runtimeID, controllers := range prior {
		runtimeUUID, err := util.ParseUUID(runtimeID)
		if err != nil {
			continue
		}
		rt, err := h.Queries.GetAgentRuntime(ctx, runtimeUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			slog.Warn("load runtime after mirror control disconnect failed", "runtime_id", runtimeID, "error", err)
			continue
		}
		if !rt.DaemonID.Valid || rt.DaemonID.String != identity.DaemonID || !rt.OwnerID.Valid {
			continue
		}
		h.publishRuntimeMirrorControl(rt, nil)
		for _, controller := range controllers {
			payload := protocol.MirrorControlStatePayload{
				WorkspaceID: util.UUIDToString(rt.WorkspaceID),
				RuntimeID:   runtimeID,
				DaemonID:    identity.DaemonID,
				ViewerID:    controller.ViewerID,
				UserID:      controller.UserID,
				Source:      controller.Source,
			}
			userUUID, err := util.ParseUUID(controller.UserID)
			if err != nil {
				slog.Warn("mirror control disconnect has invalid controller", "runtime_id", runtimeID, "error", err)
				continue
			}
			if err := h.createMirrorControlNotice(ctx, rt, userUUID, false, payload); err != nil {
				slog.Warn("mirror control disconnect inbox write failed", "runtime_id", runtimeID, "error", err)
			}
		}
	}
}

func (h *Handler) createMirrorControlNotice(
	ctx context.Context,
	rt db.AgentRuntime,
	controllerUUID pgtype.UUID,
	active bool,
	payload protocol.MirrorControlStatePayload,
) error {
	state := "stopped"
	itemType := protocol.InboxTypeRuntimeControlStopped
	name := mirrorRuntimeNoticeName(rt)
	title := "Remote control ended"
	body := fmt.Sprintf("Remote control for %s has ended.", name)
	if active {
		state = "started"
		itemType = protocol.InboxTypeRuntimeControlStarted
		title = "Remote control started"
		controllerName := "A workspace member"
		users, err := h.Queries.GetUsersByIDs(ctx, []pgtype.UUID{controllerUUID, rt.OwnerID})
		if err != nil {
			return fmt.Errorf("load mirror controller user: %w", err)
		}
		for _, user := range users {
			if user.ID == controllerUUID {
				if trimmed := strings.TrimSpace(user.Name); trimmed != "" {
					controllerName = trimmed
				}
				break
			}
		}
		body = fmt.Sprintf("%s is controlling %s.", controllerName, name)
	}
	detailsJSON, err := json.Marshal(mirrorControlDetails{
		RuntimeID:        util.UUIDToString(rt.ID),
		RuntimeName:      name,
		State:            state,
		ControllerUserID: payload.UserID,
		ViewerID:         payload.ViewerID,
		SourceKind:       string(payload.Source.Kind),
		SourceID:         payload.Source.SourceID,
	})
	if err != nil {
		return err
	}
	severity := "info"
	if active {
		severity = "attention"
	}
	item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   rt.WorkspaceID,
		RecipientType: "member",
		RecipientID:   rt.OwnerID,
		Type:          itemType,
		Severity:      severity,
		IssueID:       pgtype.UUID{},
		Title:         title,
		Body:          pgtype.Text{String: body, Valid: true},
		ActorType:     pgtype.Text{String: "system", Valid: true},
		ActorID:       pgtype.UUID{},
		Details:       detailsJSON,
	})
	if err != nil {
		return fmt.Errorf("create mirror control inbox item: %w", err)
	}
	h.publish(protocol.EventInboxNew, util.UUIDToString(rt.WorkspaceID), "system", "", map[string]any{
		"item": inboxToResponse(item),
	})
	return nil
}
