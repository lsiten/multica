// Package llm2jev contains the provider-independent semantic decision
// contract. It deliberately does not call a model or expose credentials.
package llm2jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	ModeSemanticRuntime  = "semantic_runtime"
	ModeExactLogit       = "exact_logit"
	VerdictYes           = "yes"
	VerdictNo            = "no"
	VerdictUncertain     = "uncertain"
	CompletionSatisfied  = "satisfied"
	CompletionIncomplete = "incomplete"
	CompletionUncertain  = "uncertain"

	maxReasonCodeLength = 1024
)

var (
	ErrInvalidRequest  = errors.New("llm2jev: invalid decision request")
	ErrInvalidResponse = errors.New("llm2jev: invalid decision response")
)

type Capabilities struct {
	Mode                   string `json:"mode"`
	SupportsLogprobs       bool   `json:"supports_logprobs"`
	SupportsStructuredJSON bool   `json:"supports_structured_output"`
	SupportsImages         bool   `json:"supports_images"`
}

type Candidate struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type Request struct {
	Question   string      `json:"question"`
	Evidence   []string    `json:"evidence,omitempty"`
	Candidates []Candidate `json:"candidates"`
}

type Decision struct {
	CandidateID string `json:"candidate_id"`
	Verdict     string `json:"verdict"`
	ReasonCode  string `json:"reason_code,omitempty"`
}

type Response struct {
	Mode       string     `json:"mode"`
	Calibrated bool       `json:"calibrated"`
	Decisions  []Decision `json:"decisions"`
}

type CompletionRequest struct {
	Goal     string   `json:"goal"`
	Criteria []string `json:"criteria"`
	Evidence []string `json:"evidence,omitempty"`
}

type CompletionResponse struct {
	Mode       string   `json:"mode"`
	Calibrated bool     `json:"calibrated"`
	Verdict    string   `json:"verdict"`
	Missing    []string `json:"missing,omitempty"`
	ReasonCode string   `json:"reason_code,omitempty"`
}

type Provider interface {
	Capabilities() Capabilities
	Evaluate(ctx context.Context, request Request) (Response, error)
}

func SemanticCapabilities() Capabilities {
	return Capabilities{Mode: ModeSemanticRuntime, SupportsStructuredJSON: true}
}

func ValidateRequest(request Request) error {
	if strings.TrimSpace(request.Question) == "" || len(request.Candidates) == 0 || len(request.Candidates) > 32 {
		return ErrInvalidRequest
	}
	if len(request.Question) > 16_000 || len(request.Evidence) > 64 {
		return ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || len(candidate.ID) > 256 || strings.TrimSpace(candidate.Content) == "" || len(candidate.Content) > 16_000 {
			return ErrInvalidRequest
		}
		if _, ok := seen[candidate.ID]; ok {
			return fmt.Errorf("%w: duplicate candidate id", ErrInvalidRequest)
		}
		seen[candidate.ID] = struct{}{}
	}
	for _, evidence := range request.Evidence {
		if len(evidence) > 12_000 {
			return ErrInvalidRequest
		}
	}
	return nil
}

func ValidateCompletionRequest(request CompletionRequest) error {
	if strings.TrimSpace(request.Goal) == "" || len(request.Goal) > 16_000 || len(request.Criteria) == 0 || len(request.Criteria) > 64 {
		return ErrInvalidRequest
	}
	if len(request.Evidence) > 64 {
		return ErrInvalidRequest
	}
	for _, criterion := range request.Criteria {
		if strings.TrimSpace(criterion) == "" || len(criterion) > 12_000 {
			return ErrInvalidRequest
		}
	}
	for _, evidence := range request.Evidence {
		if len(evidence) > 12_000 {
			return ErrInvalidRequest
		}
	}
	return nil
}

func NormalizeModelResponse(raw []byte, request Request) (Response, error) {
	if err := ValidateRequest(request); err != nil {
		return Response{}, err
	}
	var model struct {
		Decisions []Decision `json:"decisions"`
	}
	if err := strictJSON(raw, &model); err != nil || len(model.Decisions) != len(request.Candidates) {
		return Response{}, ErrInvalidResponse
	}
	wanted := make(map[string]struct{}, len(request.Candidates))
	for _, candidate := range request.Candidates {
		wanted[candidate.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(model.Decisions))
	for _, decision := range model.Decisions {
		if _, ok := wanted[decision.CandidateID]; !ok {
			return Response{}, fmt.Errorf("%w: unknown candidate", ErrInvalidResponse)
		}
		if _, duplicate := seen[decision.CandidateID]; duplicate {
			return Response{}, fmt.Errorf("%w: duplicate candidate", ErrInvalidResponse)
		}
		if len(decision.ReasonCode) > maxReasonCodeLength {
			return Response{}, fmt.Errorf("%w: reason code too long", ErrInvalidResponse)
		}
		seen[decision.CandidateID] = struct{}{}
		switch decision.Verdict {
		case VerdictYes, VerdictNo, VerdictUncertain:
		default:
			return Response{}, fmt.Errorf("%w: invalid verdict", ErrInvalidResponse)
		}
	}
	return Response{Mode: ModeSemanticRuntime, Calibrated: false, Decisions: model.Decisions}, nil
}

func NormalizeCompletionResponse(raw []byte, request CompletionRequest) (CompletionResponse, error) {
	if err := ValidateCompletionRequest(request); err != nil {
		return CompletionResponse{}, err
	}
	var model struct {
		Verdict    string   `json:"verdict"`
		Missing    []string `json:"missing"`
		ReasonCode string   `json:"reason_code"`
	}
	if err := strictJSON(raw, &model); err != nil {
		return CompletionResponse{}, ErrInvalidResponse
	}
	switch model.Verdict {
	case CompletionSatisfied, CompletionIncomplete, CompletionUncertain:
	default:
		return CompletionResponse{}, fmt.Errorf("%w: invalid completion verdict", ErrInvalidResponse)
	}
	if len(model.Missing) > len(request.Criteria) || len(model.ReasonCode) > maxReasonCodeLength {
		return CompletionResponse{}, ErrInvalidResponse
	}
	return CompletionResponse{Mode: ModeSemanticRuntime, Calibrated: false, Verdict: model.Verdict, Missing: model.Missing, ReasonCode: model.ReasonCode}, nil
}

func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}
