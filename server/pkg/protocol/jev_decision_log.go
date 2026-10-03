package protocol

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// JevDecisionRequestLog retains one model attempt, including rejected retries.
type JevDecisionRequestLog struct {
	Attempt            int    `json:"attempt"`
	Variant            string `json:"variant"`
	HTTPStatus         int    `json:"http_status"`
	ResultClass        string `json:"result_class"`
	DurationMS         int64  `json:"duration_ms"`
	Input              string `json:"input"`
	Output             string `json:"output"`
	ResponseIncomplete bool   `json:"response_incomplete"`
}

// JevDecisionLog is a redacted decision snapshot. Task and workspace identity
// are resolved by the server from the authenticated reporting task.
type JevDecisionLog struct {
	ID             string                  `json:"id"`
	Tool           string                  `json:"tool"`
	Source         string                  `json:"source"`
	Model          string                  `json:"model"`
	ModelRevision  string                  `json:"model_revision"`
	ConfigRevision int64                   `json:"config_revision"`
	Device         string                  `json:"device"`
	StartedAt      time.Time               `json:"started_at"`
	CompletedAt    *time.Time              `json:"completed_at"`
	DurationMS     int64                   `json:"duration_ms"`
	ResultClass    string                  `json:"result_class"`
	ErrorCode      string                  `json:"error_code"`
	Input          string                  `json:"input"`
	Output         string                  `json:"output"`
	Requests       []JevDecisionRequestLog `json:"requests"`
}

// Validate checks the bounded wire contract before persisting a snapshot.
func (r JevDecisionLog) Validate() error {
	invalid := errors.New("invalid Jev decision log")
	if _, err := uuid.Parse(r.ID); err != nil || r.StartedAt.IsZero() || len(r.Model) > 512 || len(r.ModelRevision) > 256 || r.ConfigRevision < 0 || len(r.Device) > 64 || len(r.ErrorCode) > 256 || r.DurationMS < 0 || len(r.Input) > 8<<20 || len(r.Output) > 16<<20 || len(r.Requests) > 4 {
		return invalid
	}
	switch r.Tool {
	case "multica_jev_systemone", "multica_llm2jev_evaluate", "multica_llm2jev_verify_completion":
	default:
		return invalid
	}
	switch r.Source {
	case "agent_context", "local", "remote", "system_one":
	default:
		return invalid
	}
	switch r.ResultClass {
	case "running":
		if r.CompletedAt != nil || len(r.Requests) != 0 || r.DurationMS != 0 {
			return invalid
		}
	case "success", "error", "rejected":
		if r.CompletedAt == nil || r.CompletedAt.Before(r.StartedAt) {
			return invalid
		}
	default:
		return invalid
	}
	for index, request := range r.Requests {
		if request.Attempt != index+1 || request.DurationMS < 0 || request.HTTPStatus < 0 || request.HTTPStatus > 599 || len(request.Variant) > 64 || len(request.ResultClass) > 64 || len(request.Input) > 8<<20 || len(request.Output) > 16<<20 {
			return invalid
		}
	}
	return nil
}
