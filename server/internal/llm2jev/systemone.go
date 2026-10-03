package llm2jev

import (
	"bytes"
	"encoding/json"
	"errors"
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
	if len(state) == 0 || len(state) > 256*1024 || !json.Valid(state) || (state[0] != '"' && state[0] != '{' && state[0] != '[') {
		return invalidInput("state", "provide a string, object or array of actual evidence, at most 256 KiB")
	}
	if len(r.Questions) == 0 || len(r.Questions) > 64 {
		return invalidInput("questions", "provide an object containing 1 to 64 named questions")
	}
	for id, q := range r.Questions {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return invalidInput("questions", "question keys must be nonempty and at most 256 bytes")
		}
		if err := q.validate(); err != nil {
			var fieldError *InputError
			if errors.As(err, &fieldError) {
				return invalidInput("questions.*."+fieldError.Field, fieldError.Expected)
			}
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
		return invalidInput("criteria", "instructions and criteria together must not exceed 16 KiB")
	}
	if len(q.Instructions) > 0 && !json.Valid(q.Instructions) {
		return invalidInput("instructions", "provide a valid JSON description")
	}
	switch q.Type {
	case "choice":
		var criteria map[string]json.RawMessage
		if !hasDescription(q.Instructions) {
			return invalidInput("instructions", "choice requires a question description")
		}
		if json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 255 {
			return invalidInput("criteria", "choice requires an object mapping 2 to 255 named options to descriptions")
		}
		for key := range criteria {
			if strings.TrimSpace(key) == "" || len(key) > 256 {
				return invalidInput("criteria", "choice option keys must be nonempty and at most 256 bytes")
			}
		}
	case "score":
		var criteria []json.RawMessage
		if !hasDescription(q.Instructions) {
			return invalidInput("instructions", "score requires a question description")
		}
		if json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 10 {
			return invalidInput("criteria", "score requires an ordered array of 2 to 10 level descriptions")
		}
	case "noul":
		var criteria map[string]json.RawMessage
		if len(q.Criteria) > 0 && json.Unmarshal(q.Criteria, &criteria) != nil {
			return invalidInput("criteria", "noul criteria must be an object containing only true and false descriptions")
		}
		for key := range criteria {
			if key != "true" && key != "false" {
				return invalidInput("criteria", "noul criteria keys must be true and false, not yes and no")
			}
		}
		if !hasDescription(q.Instructions) && !hasDescription(criteria["true"]) && !hasDescription(criteria["false"]) {
			return invalidInput("instructions", "noul requires instructions or a true/false criterion description")
		}
	default:
		return invalidInput("type", "use choice, score or noul")
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
