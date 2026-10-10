package daemon

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// EvaluationContext is the minimum typed boundary the task owner (control until
// F3, the task worker afterwards) hands to the AI service for one JEV
// evaluation. It is deliberately narrow: the AI receives only the owner
// namespace, the resolved source/model identity, one resolved model credential
// for remote/agent_context, limits, budget/usage policy and explicit logging
// metadata. It never carries the full Task/Agent/Config, CustomEnv, an account
// PAT or unrelated MCP credentials; the raw agent environment and the model
// lease stay in their own owners. Local selection obtains its lease inside AI,
// so it carries neither endpoint nor credential.
type EvaluationContext struct {
	// Stable owner namespace. Profile is the raw profile: empty remains
	// distinct and is never collapsed to "default".
	Backend      string
	Account      string
	Profile      string
	DaemonID     string
	WorkspaceID  string
	RuntimeID    string
	TaskID       string
	DispatchedAt time.Time

	// ExecutionIdentity is optional and server-issued; it is never fabricated
	// for a pre-existing run.
	ExecutionIdentity *protocol.ExecutionIdentity

	// ProducerAI and ContextGeneration are the AI instance and the logical
	// context generation; both are separate from execution authority and must
	// not be confused with the ExecutionIdentity.
	ProducerAI        string
	ContextGeneration int64

	// GoalAnchors are deduplicated trimmed IssueID/IssueIdentifier. They never
	// include the task prompt or history; completion arguments still supply the
	// actual criteria/evidence.
	GoalAnchors []string

	// Resolved source identity.
	Source         string // "local", "remote", "agent_context"
	ProtocolMode   string // "semantic" or "systemone"
	Provider       string
	ModelID        string
	ModelRevision  string
	Device         string
	ConfigRevision int64
	// ResolvedEndpoint and ResolvedCredential are populated only for
	// remote/agent_context. Local carries neither and obtains its lease in AI.
	ResolvedEndpoint   string
	ResolvedCredential string

	// Limits and budget.
	MaxConcurrent        int
	MaxCalls             int64
	CallTimeout          time.Duration
	ContextExpiry        time.Time
	RevocationGeneration int64
	// BudgetPolicyRevision is the task-owner budget/policy revision and whether
	// token/cost usage is required. Immutable limits alone do not authorize
	// spending; only an owner admission does.
	BudgetPolicyRevision int64
	UsageRequired        bool

	// Explicit logging metadata only. These are the only identifiers allowed to
	// ride the boundary for logs/outbox; they never re-expose a Task.
	LogIssueID          string
	LogIssueIdentifier  string
	LogProjectID        string
	LogAgentID          string
	LogAgentName        string
	LogChatSessionID    string
	LogProviderRevision string
	LogModelRevision    string
}

const (
	evalContextMaxGoalAnchors     = 64
	evalContextMaxGoalAnchorBytes = 12000
)

func (c *EvaluationContext) isRemote() bool {
	return c.Source == "remote" || c.Source == "agent_context"
}

// Validate enforces the F2 boundary invariants. It is intentionally a superset
// of the already-validated WorkspaceJevConfig, never stricter for a valid
// config, so the hot path stays safe while the boundary is exercised.
func (c *EvaluationContext) Validate() error {
	if c == nil {
		return fmt.Errorf("evaluation context is nil")
	}
	if strings.TrimSpace(c.TaskID) == "" {
		return fmt.Errorf("evaluation context has no task id")
	}
	if strings.TrimSpace(c.Provider) == "" {
		return fmt.Errorf("evaluation context has no provider")
	}
	switch c.Source {
	case "local", "remote", "agent_context":
	default:
		return fmt.Errorf("evaluation context has unsupported source %q", c.Source)
	}
	switch c.ProtocolMode {
	case "semantic", "systemone":
	default:
		return fmt.Errorf("evaluation context has unsupported protocol mode %q", c.ProtocolMode)
	}
	if c.MaxConcurrent <= 0 {
		return fmt.Errorf("evaluation context has no concurrent calls")
	}
	if c.MaxCalls <= 0 {
		return fmt.Errorf("evaluation context has no call allowance")
	}
	if c.CallTimeout <= 0 {
		return fmt.Errorf("evaluation context has no call timeout")
	}
	if len(c.GoalAnchors) > evalContextMaxGoalAnchors {
		return fmt.Errorf("evaluation context has %d goal anchors, over the limit of %d", len(c.GoalAnchors), evalContextMaxGoalAnchors)
	}
	for _, anchor := range c.GoalAnchors {
		if strings.TrimSpace(anchor) == "" {
			return fmt.Errorf("evaluation context has an empty goal anchor")
		}
		if len([]byte(anchor)) > evalContextMaxGoalAnchorBytes {
			return fmt.Errorf("evaluation context has a goal anchor over %d bytes", evalContextMaxGoalAnchorBytes)
		}
	}
	// One resolved model credential only for remote/agent_context; local must
	// carry neither endpoint nor credential because it leases inside AI.
	if c.isRemote() {
		if _, err := url.Parse(c.ResolvedEndpoint); err != nil {
			return fmt.Errorf("evaluation context has an invalid remote endpoint: %w", err)
		}
		if strings.TrimSpace(c.ResolvedEndpoint) == "" {
			return fmt.Errorf("evaluation context for source %q has no resolved endpoint", c.Source)
		}
	} else {
		if c.ResolvedEndpoint != "" || c.ResolvedCredential != "" {
			return fmt.Errorf("local evaluation context must not carry a resolved endpoint or credential")
		}
	}
	return nil
}

// buildEvaluationContext derives the boundary from the resolved source already
// carried in effective.Agent.CustomEnv. It is the owner's description of the
// evaluation, not a copy of the Task.
func (d *Daemon) buildEvaluationContext(effective Task, provider string, cfg *protocol.WorkspaceJevConfig, resolvedEndpoint, resolvedCredential string) *EvaluationContext {
	ec := &EvaluationContext{
		Backend:       strings.TrimRight(d.cfg.ServerBaseURL, "/"),
		Account:       d.accountID,
		Profile:       d.cfg.Profile,
		DaemonID:      d.cfg.DaemonID,
		WorkspaceID:   effective.WorkspaceID,
		RuntimeID:     effective.RuntimeID,
		TaskID:        effective.ID,
		Provider:      provider,
		GoalAnchors:   completionGoalAnchors(effective),
		MaxConcurrent: d.cfg.LLM2JevMaxConcurrency,
		// MaxCalls mirrors the configured JEV call allowance the hot path hands to
		// the MCP server (concurrency x 32); without it the derived context would
		// fail Validate and the boundary could never be exercised as a valid context.
		MaxCalls:      int64(d.cfg.LLM2JevMaxConcurrency) * 32,
		CallTimeout:   d.cfg.LLM2JevTimeout,
		UsageRequired: true,
	}
	if effective.DispatchedAt != "" {
		if t, err := time.Parse(time.RFC3339, effective.DispatchedAt); err == nil {
			ec.DispatchedAt = t
		}
	}
	if cfg != nil {
		ec.ConfigRevision = cfg.Revision
		ec.Source = cfg.Source
		ec.ModelRevision = cfg.ModelRevision
		ec.Device = cfg.Device
	}
	ec.ProtocolMode = protocolModeFor(effective)
	ec.ModelID = effective.Agent.Model
	if cfg != nil && cfg.ModelID != "" {
		ec.ModelID = cfg.ModelID
	}
	ec.LogAgentID = effective.AgentID
	if effective.Agent != nil {
		ec.LogAgentName = effective.Agent.Name
	}
	ec.LogIssueID = effective.IssueID
	ec.LogIssueIdentifier = effective.IssueIdentifier
	ec.LogChatSessionID = effective.ChatSessionID
	ec.LogModelRevision = ec.ModelRevision
	if ec.isRemote() {
		ec.ResolvedEndpoint = resolvedEndpoint
		ec.ResolvedCredential = resolvedCredential
	}
	return ec
}

// protocolModeFor resolves the semantic/SystemOne mode from the resolved
// environment the same way the MCP server does, so the boundary and the actual
// evaluation agree instead of each inferring mode independently.
func protocolModeFor(task Task) string {
	if task.Agent != nil && task.Agent.CustomEnv["MULTICA_JEV_SYSTEMONE"] == "1" {
		return "systemone"
	}
	return "semantic"
}
