package handler

import (
	"context"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DaemonVscreenDisabled commits cleanup only for the current authenticated socket
// and a fresh disabled native snapshot. It never queues or resumes a task.
func (h *Handler) DaemonVscreenDisabled(ctx context.Context, connection daemonws.VscreenConnection, receipt protocol.VscreenCommandReceipt) error {
	return connection.WithVscreenLifecycle(ctx, func() error { return h.confirmVscreenDisabled(ctx, connection, receipt) })
}
func (h *Handler) confirmVscreenDisabled(ctx context.Context, connection daemonws.VscreenConnection, receipt protocol.VscreenCommandReceipt) error {
	if receipt.Validate() != nil || receipt.State != protocol.VscreenReceiptSucceeded {
		return protocol.ErrInvalidVscreenContract
	}
	runtimeID, err := util.ParseUUID(receipt.RuntimeID)
	if err != nil {
		return err
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeID)
	if err != nil {
		return err
	}
	identity := connection.Identity()
	if uuidToString(rt.WorkspaceID) != receipt.WorkspaceID || !rt.DaemonID.Valid || rt.DaemonID.String != identity.DaemonID || (identity.UserID != "" && identity.UserID != uuidToString(rt.OwnerID)) {
		return service.InterventionError("permission_denied")
	}
	current, err := h.DaemonHub.QueryVscreen(ctx, receipt.WorkspaceID, receipt.RuntimeID, identity.DaemonID, "state")
	if err != nil {
		return err
	}
	if current.Reason != "" || current.DaemonGeneration != receipt.DaemonGeneration || current.State == nil || current.State.State != protocol.VscreenStateDisabled || current.State.ActiveTaskID != nil {
		return service.InterventionError("stale_generation")
	}
	return connection.WithCurrent(ctx, func() error { return h.TaskService.RetireDisabledVscreenInterventions(ctx, rt, identity.DaemonID) })
}
