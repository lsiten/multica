package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/redact"
)

// IssueNextStep is a run-scoped handoff, not permission to perform an action.
type IssueNextStep struct {
	Kind             string   `json:"kind"`
	Summary          string   `json:"summary"`
	ActorType        string   `json:"actor_type"`
	ActorID          string   `json:"actor_id,omitempty"`
	Missing          []string `json:"missing,omitempty"`
	RequestID        string   `json:"request_id,omitempty"`
	Evidence         []string `json:"evidence,omitempty"`
	IssueRevision    int64    `json:"issue_revision"`
	ScopeFingerprint string   `json:"scope_fingerprint,omitempty"`
	SourceTaskID     string   `json:"source_task_id,omitempty"`
}

// Validate rejects handoffs that do not explain the next required work.
func (step IssueNextStep) Validate() error {
	switch step.Kind {
	case "needs_information", "manual_action", "awaiting_decision", "review", "continue", "blocked", "complete":
	default:
		return fmt.Errorf("invalid next-step kind")
	}
	if !shortText(step.Summary, 500) || step.IssueRevision < 1 || len(step.Missing) > 8 || len(step.Evidence) > 8 {
		return fmt.Errorf("next step requires a concise summary and current issue revision")
	}
	switch step.ActorType {
	case "member", "agent":
		if step.ActorID == "" {
			return fmt.Errorf("next-step actor is required")
		}
	case "unknown":
		if step.ActorID != "" {
			return fmt.Errorf("unknown actor cannot carry an ID")
		}
	default:
		return fmt.Errorf("invalid next-step actor")
	}
	for _, field := range append(append([]string{}, step.Missing...), step.Evidence...) {
		if !shortText(field, 240) {
			return fmt.Errorf("next-step fields must be concise")
		}
	}
	if step.Kind == "needs_information" && len(step.Missing) == 0 {
		return fmt.Errorf("name the missing information")
	}
	if (step.Kind == "manual_action" || step.Kind == "awaiting_decision") && step.RequestID == "" {
		return fmt.Errorf("a formal human request is required")
	}
	return nil
}

func parseIssueNextStep(raw []byte, revision int64, runID, scopeFingerprint string) *IssueNextStep {
	var context struct {
		NextStep *IssueNextStep `json:"next_step"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &context) != nil || context.NextStep == nil {
		return nil
	}
	step := context.NextStep
	if step.Validate() != nil || step.IssueRevision > revision || step.SourceTaskID != runID || scopeFingerprint == "" || step.ScopeFingerprint != scopeFingerprint {
		return nil
	}
	return step
}

func progressDeliverySummary(raw []byte) string {
	var result struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal(raw, &result) != nil || !utf8.ValidString(result.Summary) {
		return ""
	}
	text := redact.Text(strings.TrimSpace(result.Summary))
	runes := []rune(text)
	if len(runes) > 1200 {
		return string(runes[:1200]) + "..."
	}
	return text
}
