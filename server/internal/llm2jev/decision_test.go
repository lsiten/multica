package llm2jev

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeModelResponseRequiresEveryCandidate(t *testing.T) {
	request := Request{
		Question:   "Does the evidence support the candidates?",
		Evidence:   []string{"candidate a is supported"},
		Candidates: []Candidate{{ID: "a", Content: "supported"}, {ID: "b", Content: "unknown"}},
	}
	response, err := NormalizeModelResponse([]byte("{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"},{\"candidate_id\":\"b\",\"verdict\":\"uncertain\"}]}"), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != ModeSemanticRuntime || response.Calibrated || len(response.Decisions) != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestNormalizeModelResponsePreservesOptionalConfidenceAndProbabilities(t *testing.T) {
	request := Request{Question: "q", Candidates: []Candidate{{ID: "a", Content: "x"}}}
	response, err := NormalizeModelResponse([]byte(`{"confidence":0.8,"probabilities":{"a":0.8},"decisions":[{"candidate_id":"a","verdict":"yes"}]}`), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Confidence == nil || *response.Confidence != 0.8 || response.Probabilities["a"] != 0.8 {
		t.Fatalf("optional decision metadata was lost: %+v", response)
	}
}

func TestNormalizeModelResponseRejectsUnknownDuplicateAndInvalidVerdicts(t *testing.T) {
	request := Request{Question: "q", Candidates: []Candidate{{ID: "a", Content: "x"}}}
	for _, raw := range []string{
		"{\"decisions\":[{\"candidate_id\":\"b\",\"verdict\":\"yes\"}]}",
		"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"},{\"candidate_id\":\"a\",\"verdict\":\"no\"}]}",
		"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"maybe\"}]}",
	} {
		if _, err := NormalizeModelResponse([]byte(raw), request); err == nil {
			t.Fatalf("response %s unexpectedly accepted", raw)
		}
	}
}

func TestNormalizeModelResponseRejectsOversizedReasonCode(t *testing.T) {
	request := Request{Question: "q", Candidates: []Candidate{{ID: "a", Content: "x"}}}
	raw := "{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\",\"reason_code\":\"" + strings.Repeat("x", 1025) + "\"}]}"
	if _, err := NormalizeModelResponse([]byte(raw), request); err == nil {
		t.Fatal("oversized reason code was accepted")
	}
}

func TestValidateRequestRejectsDuplicateAndOversizedInput(t *testing.T) {
	cases := []Request{
		{Question: "q", Candidates: []Candidate{{ID: "a", Content: "x"}, {ID: "a", Content: "y"}}},
		{Question: "q", Candidates: []Candidate{{ID: "a", Content: ""}}},
		{Question: string(make([]byte, 16001)), Candidates: []Candidate{{ID: "a", Content: "x"}}},
	}
	for _, request := range cases {
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("request unexpectedly accepted: %+v", request)
		}
	}
}

func TestValidateRequestUsesStableSentinel(t *testing.T) {
	err := ValidateRequest(Request{Question: "q"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v, want ErrInvalidRequest", err)
	}
}

func TestNormalizeCompletionResponse(t *testing.T) {
	request := CompletionRequest{Goal: "finish report", Criteria: []string{"report exists", "tests pass"}, Evidence: []string{"report.md", "go test passed"}}
	response, err := NormalizeCompletionResponse([]byte("{\"verdict\":\"satisfied\",\"missing\":[],\"reason_code\":\"evidence_match\"}"), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != ModeSemanticRuntime || response.Verdict != CompletionSatisfied || response.Calibrated {
		t.Fatalf("unexpected completion response: %+v", response)
	}
}

func TestNormalizeCompletionResponsePreservesOptionalConfidence(t *testing.T) {
	request := CompletionRequest{Goal: "finish", Criteria: []string{"done"}}
	response, err := NormalizeCompletionResponse([]byte(`{"verdict":"satisfied","confidence":0.9,"probabilities":{"satisfied":0.9,"incomplete":0.1}}`), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Confidence == nil || *response.Confidence != 0.9 || response.Probabilities["satisfied"] != 0.9 {
		t.Fatalf("optional completion metadata was lost: %+v", response)
	}
}

func TestNormalizeCompletionResponseRejectsInvalidVerdict(t *testing.T) {
	request := CompletionRequest{Goal: "finish", Criteria: []string{"done"}}
	if _, err := NormalizeCompletionResponse([]byte("{\"verdict\":\"maybe\"}"), request); err == nil {
		t.Fatal("invalid completion verdict was accepted")
	}
}
