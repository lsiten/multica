package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type mirrorChildReports struct{ service *mirrorProcessService }

func (r *mirrorChildReports) call(ctx context.Context, operation string, input mirrorReportRequest) (json.RawMessage, error) {
	r.service.mu.Lock()
	generation := r.service.generation
	r.service.mu.Unlock()
	result, _, err := r.service.events.emit(ctx, generation, operation, input)
	return result, err
}
func (r *mirrorChildReports) Queue(ctx context.Context, report protocol.VscreenIntervention) error {
	_, err := r.call(ctx, "report_queue", mirrorReportRequest{Report: &report})
	return err
}
func (r *mirrorChildReports) Acknowledged(id string, state protocol.VscreenInterventionState) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := r.call(ctx, "report_acknowledged", mirrorReportRequest{InterventionID: id, State: state})
	var answer struct {
		Acknowledged bool `json:"acknowledged"`
	}
	return err == nil && json.Unmarshal(raw, &answer) == nil && answer.Acknowledged
}
func (r *mirrorChildReports) Consume(workspace, runtime string, proof protocol.VscreenContinuationContext) error {
	_, err := r.call(context.Background(), "report_consume", mirrorReportRequest{WorkspaceID: workspace, RuntimeID: runtime, Proof: &proof})
	return err
}
func (r *mirrorChildReports) CancelScope(workspace, runtime string) error {
	_, err := r.call(context.Background(), "report_cancel", mirrorReportRequest{WorkspaceID: workspace, RuntimeID: runtime})
	return err
}
func (r *mirrorChildReports) InvalidateEpoch(workspace, runtime, epoch string) error {
	_, err := r.call(context.Background(), "report_epoch", mirrorReportRequest{WorkspaceID: workspace, RuntimeID: runtime, Epoch: epoch})
	return err
}
