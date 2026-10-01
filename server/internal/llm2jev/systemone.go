package llm2jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SystemOneRequest follows the pinned decider-ai SystemOne contract. Criteria,
// not a chat schema's options/legend fields, carry described alternatives.
type SystemOneRequest struct {
	State     json.RawMessage              `json:"state"`
	Questions map[string]SystemOneQuestion `json:"questions"`
}
type SystemOneQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions,omitempty"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

func (r SystemOneRequest) Validate() error {
	state := bytes.TrimSpace(r.State)
	if len(state) == 0 || len(state) > 256*1024 || !json.Valid(state) || (state[0] != '"' && state[0] != '{' && state[0] != '[') || len(r.Questions) == 0 || len(r.Questions) > 64 {
		return ErrInvalidRequest
	}
	for id, q := range r.Questions {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return ErrInvalidRequest
		}
		if err := q.validate(); err != nil {
			return fmt.Errorf("%w: question %q", err, id)
		}
	}
	return nil
}
func hasDescription(raw json.RawMessage) bool {
	s := string(bytes.TrimSpace(raw))
	return s != "" && s != "null" && s != `""`
}
func (q SystemOneQuestion) validate() error {
	if len(q.Instructions)+len(q.Criteria) > 16*1024 {
		return ErrInvalidRequest
	}
	if len(q.Instructions) > 0 && !json.Valid(q.Instructions) {
		return ErrInvalidRequest
	}
	switch q.Type {
	case "choice":
		var criteria map[string]json.RawMessage
		if !hasDescription(q.Instructions) || json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 255 {
			return ErrInvalidRequest
		}
		for key := range criteria {
			if strings.TrimSpace(key) == "" || len(key) > 256 {
				return ErrInvalidRequest
			}
		}
	case "score":
		var criteria []json.RawMessage
		if !hasDescription(q.Instructions) || json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 10 {
			return ErrInvalidRequest
		}
	case "noul":
		var criteria map[string]json.RawMessage
		if len(q.Criteria) > 0 && json.Unmarshal(q.Criteria, &criteria) != nil {
			return ErrInvalidRequest
		}
		for key := range criteria {
			if key != "true" && key != "false" {
				return ErrInvalidRequest
			}
		}
		if !hasDescription(q.Instructions) && !hasDescription(criteria["true"]) && !hasDescription(criteria["false"]) {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
func ValidateSystemOneResponse(raw []byte, request SystemOneRequest) error {
	var response struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        *string            `json:"choice"`
			Score         *float64           `json:"score"`
			Noul          *float64           `json:"noul"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Answers) != len(request.Questions) {
		return ErrInvalidResponse
	}
	for id, q := range request.Questions {
		a, ok := response.Answers[id]
		if !ok || a.Type != q.Type {
			return ErrInvalidResponse
		}
		switch q.Type {
		case "noul":
			if a.Noul == nil || !probability(*a.Noul) {
				return ErrInvalidResponse
			}
		case "choice":
			var choices map[string]json.RawMessage
			if json.Unmarshal(q.Criteria, &choices) != nil || a.Choice == nil {
				return ErrInvalidResponse
			}
			if _, ok := choices[*a.Choice]; !ok || len(a.Probabilities) != len(choices) {
				return ErrInvalidResponse
			}
			for key := range choices {
				if _, ok := a.Probabilities[key]; !ok {
					return ErrInvalidResponse
				}
			}
			if !distribution(a.Probabilities) {
				return ErrInvalidResponse
			}
		case "score":
			var levels []json.RawMessage
			if json.Unmarshal(q.Criteria, &levels) != nil || a.Score == nil || math.IsNaN(*a.Score) || *a.Score < 0 || *a.Score > float64(len(levels)-1) || len(a.Probabilities) != len(levels) {
				return ErrInvalidResponse
			}
			for i := range levels {
				if _, ok := a.Probabilities[strconv.Itoa(i)]; !ok {
					return ErrInvalidResponse
				}
			}
			if !distribution(a.Probabilities) {
				return ErrInvalidResponse
			}
		default:
			return ErrInvalidResponse
		}
	}
	return nil
}
func probability(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }
func distribution(values map[string]float64) bool {
	sum := 0.0
	for _, p := range values {
		if !probability(p) {
			return false
		}
		sum += p
	}
	// Provider rounds each of up to 255 probabilities to four decimal places.
	return math.Abs(sum-1) <= float64(len(values))*0.000051
}
