package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// reconcileMutation confirms only terminal physical journal states produced by
// this exact returned invocation. File existence alone never proves a cleanup,
// restore, or checkout succeeded, and incomplete external effects are not replayed.
func (s *environmentProcessService) reconcileMutation(ctx context.Context, request runtimeproc.Request, outcome runtimeproc.Outcome) (runtimeproc.Outcome, bool) {
	if ctx.Err() != nil || len(outcome.Result) == 0 {
		return outcome, false
	}
	id, matches := physicalReconciliationTarget(request, outcome)
	if !matches || filepath.Base(id) != id {
		return outcome, false
	}
	s.mu.RLock()
	owner := s.physical
	s.mu.RUnlock()
	if owner == nil {
		return outcome, false
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	preparation := owner.preparations[id]
	if preparation == nil || execenv.VerifyPhysicalGeneration(preparation.Result.Reservation, preparation.identity) != nil {
		return outcome, false
	}
	raw, err := os.ReadFile(filepath.Join(owner.journal, id+".json"))
	if err != nil {
		return outcome, false
	}
	var durable environmentPhysicalPreparation
	if json.Unmarshal(raw, &durable) != nil || durable.Result.Reservation != preparation.Result.Reservation || ctx.Err() != nil {
		return outcome, false
	}
	return outcome, physicalOutcomeMatches(request, outcome, &durable)
}

func physicalReconciliationTarget(request runtimeproc.Request, outcome runtimeproc.Outcome) (string, bool) {
	switch request.Operation {
	case "physical.prepare", "physical.begin_task", "physical.select":
		var result environmentPhysicalResult
		if json.Unmarshal(outcome.Result, &result) != nil || result.Reservation.ID == "" || outcome.Error != nil {
			return "", false
		}
		return result.Reservation.ID, true
	case "physical.root_claim", "physical.root_confirm":
		var reservation execenv.PhysicalRootReservation
		if json.Unmarshal(request.Payload, &reservation) != nil || reservation.ID == "" || outcome.Error != nil {
			return "", false
		}
		return reservation.ID, true
	case "physical.attach", "physical.abort", "physical.finish_begin", "physical_finish_confirm", "physical.verify_completion":
		var input struct {
			PreparationID string `json:"preparation_id"`
		}
		if json.Unmarshal(request.Payload, &input) != nil || input.PreparationID == "" {
			return "", false
		}
		return input.PreparationID, true
	default:
		return "", false
	}
}

func physicalOutcomeMatches(request runtimeproc.Request, outcome runtimeproc.Outcome, durable *environmentPhysicalPreparation) bool {
	if outcome.Error != nil && request.Operation != "physical_finish_confirm" {
		return false
	}
	switch request.Operation {
	case "physical.prepare", "physical.begin_task", "physical.select":
		var result environmentPhysicalResult
		if json.Unmarshal(outcome.Result, &result) != nil || !reflect.DeepEqual(result, durable.Result) {
			return false
		}
		if request.Operation == "physical.select" {
			var input environmentPhysicalSelection
			return json.Unmarshal(request.Payload, &input) == nil && input.PreparationID == result.Reservation.ID && durable.Selected
		}
		var input execenv.PhysicalPrepareParams
		if request.Operation == "physical.begin_task" {
			var begin struct {
				Physical execenv.PhysicalPrepareParams `json:"physical"`
			}
			if json.Unmarshal(request.Payload, &begin) != nil {
				return false
			}
			input = begin.Physical
		} else if json.Unmarshal(request.Payload, &input) != nil {
			return false
		}
		return input.TaskID == result.Reservation.TaskID && input.WorkspaceID == result.Reservation.WorkspaceID
	case "physical.root_claim", "physical.root_confirm":
		var reservation execenv.PhysicalRootReservation
		return json.Unmarshal(request.Payload, &reservation) == nil && reservation == durable.Result.Reservation && durable.RootConfirmed
	case "physical.attach":
		var input struct {
			Participant execenv.PhysicalParticipant `json:"participant"`
		}
		return json.Unmarshal(request.Payload, &input) == nil && durable.Attached && durable.Participant != nil && *durable.Participant == input.Participant
	case "physical.abort":
		return durable.Aborted && durable.Outcome != nil
	case "physical.finish_begin":
		var permit execenv.PhysicalFinishPermit
		return json.Unmarshal(outcome.Result, &permit) == nil && permit.ID != "" && durable.Permit != nil && *durable.Permit == permit
	case "physical_finish_confirm":
		var result execenv.LocalWorktreeOutcome
		var input struct {
			Permit execenv.PhysicalFinishPermit `json:"permit"`
		}
		if json.Unmarshal(request.Payload, &input) != nil || durable.Permit == nil || *durable.Permit != input.Permit || durable.Aborted || durable.Outcome == nil || json.Unmarshal(outcome.Result, &result) != nil || !reflect.DeepEqual(result, *durable.Outcome) {
			return false
		}
		if outcome.Error != nil {
			return durable.Failure != "" && durable.Failure == outcome.Error.Message
		}
		return durable.Failure == ""
	case "physical.verify_completion":
		return true
	default:
		return false
	}
}
