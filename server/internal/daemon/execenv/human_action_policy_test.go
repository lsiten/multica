package execenv

import (
	"strings"
	"testing"
)

func TestHumanActionPolicyOnlyRequiresIssueHandoffsForIssueRuns(t *testing.T) {
	for _, issueRun := range []bool{true, false} {
		var policy strings.Builder
		writeHumanActionPolicy(&policy, issueRun)
		if got := strings.Contains(policy.String(), "multica issue next-step <issue>"); got != issueRun {
			t.Errorf("issue handoff command present=%v for issueRun=%v", got, issueRun)
		}
	}
}

func TestRuntimeBriefScopesHandoffToActualRunKind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ctx   TaskContextForEnv
		issue bool
	}{
		{"issue", TaskContextForEnv{IssueID: "issue-a"}, true},
		{"chat", TaskContextForEnv{ChatSessionID: "chat-a"}, false},
		{"quick-create", TaskContextForEnv{QuickCreatePrompt: "create"}, false},
		{"automation", TaskContextForEnv{AutopilotRunID: "auto-a"}, false},
		{"human-followup", TaskContextForEnv{HumanFollowupPrompt: "follow up"}, false},
		{"supervision", TaskContextForEnv{ProjectSupervisionPrompt: "review"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brief := buildMetaSkillContent("codex", tc.ctx)
			if got := strings.Contains(brief, "multica issue next-step <issue>"); got != tc.issue {
				t.Errorf("issue handoff command present=%v, want %v", got, tc.issue)
			}
		})
	}
}
