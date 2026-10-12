package daemon

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationStepReceipt struct {
	StepID      string                         `json:"step_id"`
	WorkspaceID string                         `json:"workspace_id"`
	InstanceID  string                         `json:"instance_id"`
	Generation  int64                          `json:"generation"`
	Revision    int64                          `json:"revision"`
	Action      string                         `json:"action"`
	Result      protocol.ApplicationStepResult `json:"result"`
}

func (receipt applicationStepReceipt) validate(runtimeID, stepID string) error {
	for _, id := range []string{runtimeID, stepID, receipt.WorkspaceID, receipt.InstanceID, receipt.Result.ClaimToken} {
		if uuid.Validate(id) != nil {
			return errors.New("invalid application receipt identity")
		}
	}
	if receipt.StepID != stepID || receipt.Generation < 1 || receipt.Revision < 1 || receipt.Result.Observation.InstanceID != receipt.InstanceID || receipt.Result.Observation.Generation != receipt.Generation || receipt.Result.Observation.Revision != receipt.Revision || !slices.Contains([]string{"completed", "failed", "blocked"}, receipt.Result.State) || !slices.Contains([]string{"start", "stop", "restart", "resume", "publish", "unpublish"}, receipt.Action) {
		return errors.New("invalid application receipt scope")
	}
	return nil
}

func (d *Daemon) queueApplicationStep(runtimeID string, claim protocol.ApplicationClaim, result protocol.ApplicationStepResult) error {
	command := claim.Command
	if command.RuntimeID != runtimeID || claim.ClaimToken != result.ClaimToken {
		return errors.New("application receipt claim changed")
	}
	receipt := &applicationStepReceipt{StepID: claim.StepID, WorkspaceID: command.WorkspaceID, InstanceID: command.InstanceID, Generation: command.Generation, Revision: command.Revision, Action: command.Action, Result: result}
	if err := receipt.validate(runtimeID, claim.StepID); err != nil {
		return err
	}
	d.mu.Lock()
	if d.terminalReports == nil {
		d.terminalReports = newTerminalReportStore(d.cfg)
		d.terminalReports.bindAccount(d.accountID)
	}
	d.mu.Unlock()
	if d.terminalReports == nil {
		return errors.New("application receipt storage unavailable")
	}
	return d.queueTaskReport(terminalTaskReport{kind: applicationStepReport, taskID: claim.StepID, runtimeID: runtimeID, applicationStep: receipt})
}

func (d *Daemon) pendingApplicationStep(ctx context.Context, runtimeID string, claim protocol.ApplicationClaim) (*protocol.ApplicationStepResult, *pendingTerminalTaskReport, error) {
	if d.terminalReports == nil {
		return nil, nil, nil
	}
	items, err := d.terminalReports.list()
	if err != nil {
		return nil, nil, err
	}
	for _, item := range items {
		receipt := item.report.applicationStep
		command := claim.Command
		if item.report.kind != applicationStepReport || item.report.runtimeID != runtimeID || receipt.StepID != claim.StepID || receipt.WorkspaceID != command.WorkspaceID || receipt.InstanceID != command.InstanceID || receipt.Generation != command.Generation || receipt.Revision != command.Revision || receipt.Action != command.Action {
			continue
		}
		result := receipt.Result
		result.ClaimToken = claim.ClaimToken
		if result.State == "completed" && receipt.Result.ClaimToken != claim.ClaimToken {
			observation, inspectErr := d.inspectApplication(ctx, command)
			if inspectErr != nil {
				return nil, nil, inspectErr
			}
			result.Observation = observation
		}
		copyItem := item
		return &result, &copyItem, nil
	}
	return nil, nil, nil
}
