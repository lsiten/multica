package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func evalTestDaemon() *Daemon {
	return &Daemon{
		cfg: Config{
			ServerBaseURL:         "https://api.example.test",
			Profile:               "work",
			DaemonID:              "daemon-1",
			LLM2JevMaxConcurrency: 4,
			LLM2JevTimeout:        45 * time.Second,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func validEvalContext() *EvaluationContext {
	return &EvaluationContext{
		Backend:          "https://api.example.test",
		Account:          "acct-1",
		Profile:          "work",
		DaemonID:         "daemon-1",
		TaskID:           "task-1",
		Provider:         "codex",
		Source:           "remote",
		ProtocolMode:     "systemone",
		MaxConcurrent:    4,
		MaxCalls:         128,
		CallTimeout:      45 * time.Second,
		ResolvedEndpoint: "https://api.openai.com/v1",
	}
}

func TestEvaluationContextValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*EvaluationContext)
		wantErr bool
	}{
		{"valid remote", nil, false},
		{"empty task id", func(c *EvaluationContext) { c.TaskID = "" }, true},
		{"empty provider", func(c *EvaluationContext) { c.Provider = "" }, true},
		{"bad source", func(c *EvaluationContext) { c.Source = "bogus" }, true},
		{"bad protocol mode", func(c *EvaluationContext) { c.ProtocolMode = "bogus" }, true},
		{"zero concurrency", func(c *EvaluationContext) { c.MaxConcurrent = 0 }, true},
		{"zero calls", func(c *EvaluationContext) { c.MaxCalls = 0 }, true},
		{"zero timeout", func(c *EvaluationContext) { c.CallTimeout = 0 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validEvalContext()
			if tc.mutate != nil {
				tc.mutate(c)
			}
			if err := c.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// TestEvaluationContextLocalCarriesNoCredential is the core F2 invariant: a
// local selection obtains its lease inside AI, so the boundary must carry
// neither endpoint nor credential, while remote/agent_context carry exactly one
// resolved credential and endpoint.
func TestEvaluationContextLocalCarriesNoCredential(t *testing.T) {
	remote := &EvaluationContext{
		Source:           "remote",
		Provider:         "codex",
		TaskID:           "task-1",
		ProtocolMode:     "semantic",
		MaxConcurrent:    4,
		MaxCalls:         128,
		CallTimeout:      45 * time.Second,
		ResolvedEndpoint: "https://api.openai.com/v1",
	}
	if err := remote.Validate(); err != nil {
		t.Fatalf("remote: %v", err)
	}

	local := &EvaluationContext{
		Source:        "local",
		Provider:      "codex",
		TaskID:        "task-1",
		ProtocolMode:  "semantic",
		MaxConcurrent: 4,
		MaxCalls:      128,
		CallTimeout:   45 * time.Second,
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("local: %v", err)
	}
	// A local context that sneaks an endpoint or credential must be rejected.
	leaky := &EvaluationContext{
		Source:           "local",
		Provider:         "codex",
		TaskID:           "task-1",
		ProtocolMode:     "semantic",
		MaxConcurrent:    4,
		MaxCalls:         128,
		CallTimeout:      45 * time.Second,
		ResolvedEndpoint: "https://api.openai.com/v1",
	}
	if err := leaky.Validate(); err == nil {
		t.Fatal("local context carrying a resolved endpoint must fail validation")
	}
}

// TestEvaluationContextGoalAnchorLimit locks the boundary to the documented
// cap: over the anchor limit or an empty anchor is rejected, so a boundary
// cannot smuggle an unbounded or empty goal set into AI.
func TestEvaluationContextGoalAnchorLimit(t *testing.T) {
	over := validEvalContext()
	anchors := make([]string, 0, evalContextMaxGoalAnchors+1)
	for i := 0; i < evalContextMaxGoalAnchors+1; i++ {
		anchors = append(anchors, "issue-a")
	}
	over.GoalAnchors = anchors
	if err := over.Validate(); err == nil {
		t.Fatal("over-limit goal anchors must fail validation")
	}

	empty := validEvalContext()
	empty.GoalAnchors = []string{""}
	if err := empty.Validate(); err == nil {
		t.Fatal("empty goal anchor must fail validation")
	}
}

// TestEvaluationContextBuilderIsNarrow proves the owner's boundary carries the
// owner namespace, resolved source identity and logging metadata, but not the
// raw agent credentials or the full Task.
func TestEvaluationContextBuilderIsNarrow(t *testing.T) {
	d := evalTestDaemon()
	d.accountID = "acct-1"
	provider := "codex"
	cfg := &protocol.WorkspaceJevConfig{
		Source:         "remote",
		ModelID:        "gpt-5.1",
		Endpoint:       "https://api.openai.com/v1",
		CredentialEnv:  "JEV_API_KEY",
		TimeoutSeconds: 45,
		Revision:       7,
	}
	task := Task{
		ID:              "task-1",
		WorkspaceID:     "ws-1",
		RuntimeID:       "rt-1",
		AgentID:         "agent-1",
		IssueID:         "issue-1",
		IssueIdentifier: "MUL-123",
		Agent:           &AgentData{Name: "Codex", Model: "gpt-5.1", CustomEnv: map[string]string{"OPENAI_API_KEY": "secret-key"}},
	}
	ec := d.buildEvaluationContext(task, provider, cfg, "https://api.openai.com/v1", "")
	if ec.TaskID != "task-1" || ec.Account != "acct-1" || ec.Provider != provider {
		t.Fatalf("namespace not built: %+v", ec)
	}
	if ec.Source != "remote" || ec.ResolvedEndpoint != "https://api.openai.com/v1" {
		t.Fatalf("remote endpoint not resolved: %+v", ec)
	}
	if ec.LogAgentName != "Codex" || ec.LogIssueIdentifier != "MUL-123" {
		t.Fatalf("logging metadata not built: %+v", ec)
	}
	if len(ec.GoalAnchors) == 0 {
		t.Fatal("goal anchors not derived")
	}
	// The boundary must never carry the raw agent credential.
	if ec.ResolvedCredential != "" {
		t.Fatalf("boundary leaked a raw credential: %q", ec.ResolvedCredential)
	}
	// The derived context must itself be a valid boundary. In particular
	// MaxCalls must be populated from the configured call allowance; without it
	// Validate rejects the context and the hot path can only ever log a broken
	// boundary rather than exercise a valid one.
	if ec.MaxCalls <= 0 {
		t.Fatalf("derived context has no MaxCalls: %+v", ec)
	}
	if err := ec.Validate(); err != nil {
		t.Fatalf("derived context did not validate: %v", err)
	}
}
