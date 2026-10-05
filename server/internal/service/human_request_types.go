package service

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var (
	ErrHumanRequestInput     = errors.New("invalid human request")
	ErrHumanRequestConflict  = errors.New("human request changed or is no longer pending")
	ErrHumanRequestForbidden = errors.New("human request access denied")
)

const HumanFollowupContextType = "human_request_followup"

// HumanFollowupContext keeps an issue-less continuation separate from its
// original automation result.
type HumanFollowupContext struct {
	Type         string `json:"type"`
	WorkspaceID  string `json:"workspace_id"`
	AutopilotID  string `json:"autopilot_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	SourceTaskID string `json:"source_task_id"`
	Prompt       string `json:"prompt"`
}

// HumanRequestChoice is a named decision; IDs are stable within one revision.
type HumanRequestChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
}

// HumanRequestInput describes the member's action, not an executable command.
type HumanRequestInput struct {
	Key              string               `json:"key"`
	Kind             string               `json:"kind"`
	Title            string               `json:"title"`
	Steps            []string             `json:"steps,omitempty"`
	ActionLabel      string               `json:"action_label"`
	Next             string               `json:"next"`
	Impact           string               `json:"impact,omitempty"`
	Choices          []HumanRequestChoice `json:"choices,omitempty"`
	InputLabel       string               `json:"input_label,omitempty"`
	Verification     string               `json:"verification,omitempty"`
	Details          string               `json:"details,omitempty"`
	ExpiresInSeconds int64                `json:"expires_in_seconds,omitempty"`
}

// Validate rejects requests whose primary action would have to be inferred.
func (r HumanRequestInput) Validate() error {
	fail := func(field string) error { return fmt.Errorf("%w: %s", ErrHumanRequestInput, field) }
	for _, field := range []struct {
		name, value string
		max         int
	}{
		{"key", r.Key, 80}, {"title", r.Title, 100}, {"action_label", r.ActionLabel, 40}, {"next", r.Next, 240},
	} {
		if !shortText(field.value, field.max) {
			return fail(field.name + " is required and must be concise")
		}
	}
	if len(r.Steps) > 5 || utf8.RuneCountInString(r.Impact) > 400 || len(r.Details) > 8000 || utf8.RuneCountInString(r.Verification) > 500 {
		return fail("request is too long; move background into details")
	}
	for _, step := range r.Steps {
		if !shortText(step, 240) {
			return fail("steps must be concrete and concise")
		}
	}
	if r.ExpiresInSeconds != 0 && (r.ExpiresInSeconds < 300 || r.ExpiresInSeconds > 30*86400) {
		return fail("expiry must be between five minutes and thirty days")
	}
	switch r.Kind {
	case "confirmation":
		if len(r.Choices) > 0 || r.InputLabel != "" {
			return fail("confirmation cannot contain choices or an input")
		}
	case "choice":
		if len(r.Choices) < 2 || len(r.Choices) > 4 {
			return fail("provide two to four named choices")
		}
		ids, recommended := map[string]bool{}, 0
		for _, choice := range r.Choices {
			if !shortText(choice.ID, 80) || !shortText(choice.Label, 100) || ids[choice.ID] {
				return fail("choices need unique IDs and concise labels")
			}
			ids[choice.ID] = true
			if choice.Recommended {
				recommended++
			}
		}
		if recommended > 1 || r.InputLabel != "" {
			return fail("provide at most one recommendation and no free-text input")
		}
	case "input":
		if !shortText(r.InputLabel, 120) || len(r.Choices) > 0 {
			return fail("name the information needed")
		}
	case "manual":
		if len(r.Steps) == 0 || !shortText(r.Verification, 500) || len(r.Choices) > 0 || r.InputLabel != "" {
			return fail("manual action requires actual steps and a verification instruction")
		}
	default:
		return fail("kind must be confirmation, choice, input or manual")
	}
	return nil
}

func shortText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= max
}

// HumanRequestAnswer carries a revision-bound member response.
type HumanRequestAnswer struct {
	Revision int64  `json:"revision"`
	Decision string `json:"decision"`
	Answer   string `json:"answer,omitempty"`
}

// Validate binds the answer to a specific kind and its offered choices.
func (a HumanRequestAnswer) Validate(r HumanRequestInput) error {
	if a.Revision < 1 || len(a.Answer) > 8000 || !utf8.ValidString(a.Answer) || strings.ContainsRune(a.Answer, 0) {
		return ErrHumanRequestInput
	}
	if a.Decision == "reject" && a.Answer == "" {
		return nil
	}
	switch r.Kind {
	case "confirmation":
		if a.Decision == "approve" && a.Answer == "" {
			return nil
		}
	case "manual":
		if a.Decision == "completed" && a.Answer == "" {
			return nil
		}
	case "input":
		if a.Decision == "input" && strings.TrimSpace(a.Answer) != "" {
			return nil
		}
	case "choice":
		if a.Decision == "choice" {
			for _, choice := range r.Choices {
				if choice.ID == a.Answer {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%w: answer does not match the request", ErrHumanRequestInput)
}

func humanRequestText(r HumanRequestInput) string {
	var b strings.Builder
	b.WriteString(r.Title + "\n")
	for i, step := range r.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	for _, choice := range r.Choices {
		b.WriteString(choice.Label + "\n")
	}
	if r.InputLabel != "" {
		b.WriteString(r.InputLabel + "\n")
	}
	if r.Impact != "" {
		b.WriteString(r.Impact + "\n")
	}
	b.WriteString(r.Next)
	return b.String()
}

func humanAnswerText(r HumanRequestInput, a HumanRequestAnswer) string {
	// Quoted values preserve the exact decision without making member text a
	// new platform instruction. The normal issue/chat prompt remains in charge.
	text := fmt.Sprintf("Human response to %q (revision %d): %s", r.Title, a.Revision, a.Decision)
	if a.Answer != "" {
		text += fmt.Sprintf("\nAnswer: %q", a.Answer)
	}
	if r.Kind == "manual" && a.Decision == "completed" {
		text += "\nVerify before continuing: " + r.Verification
	}
	if a.Decision == "reject" {
		text += "\nDo not perform the rejected action."
	}
	return text
}
