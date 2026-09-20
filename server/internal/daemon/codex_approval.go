package daemon

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func (d *Daemon) requestTaskApproval(task Task) func(context.Context, agent.ApprovalRequest) (bool, error) {
	return func(ctx context.Context, request agent.ApprovalRequest) (bool, error) {
		if task.InitiatorType != "member" || task.InitiatorID == "" {
			return false, errors.New("daemon: no human approval initiator")
		}
		d.mu.Lock()
		rm := d.runtimeMirrors[task.RuntimeID]
		d.mu.Unlock()
		if rm == nil {
			return false, errors.New("daemon: no mirror reviewer connected")
		}
		return rm.RequestCLIApproval(ctx, mirror.CLIApprovalAudience{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, UserID: task.InitiatorID}, "Codex: "+request.Method, "Task: "+task.ID+"\n"+string(request.Params))
	}
}
