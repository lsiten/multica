package execenv

import (
	"strings"
	"testing"
)

func TestProjectSupervisionBriefHasBoundedActionsOnly(t *testing.T) {
	ctx := TaskContextForEnv{ProjectID: "project-id", ProjectTitle: "Project", ProjectSupervisionPrompt: "Resolve ready work"}
	if classifyTask(ctx) != kindProjectSupervision || classifyTask(ctx).hasIssueContext() {
		t.Fatal("supervision classified as an issue")
	}
	brief := buildMetaSkillContent("claude", ctx)
	for _, required := range []string{"project supervision get", "project supervision apply", "project supervision report", "never poll or sleep"} {
		if !strings.Contains(brief, required) {
			t.Fatalf("brief missing %q", required)
		}
	}
	for _, forbidden := range []string{"multica issue create", "multica issue assign", "multica issue status", "Sub-issue Creation"} {
		if strings.Contains(brief, forbidden) {
			t.Fatalf("brief advertises %q", forbidden)
		}
	}
}
