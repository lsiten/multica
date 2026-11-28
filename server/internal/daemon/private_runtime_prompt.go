package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// Native skill invocation rebuilds the prompt, so it needs the private brief too.
func prependRuntimeBrief(prompt string, invocation *agent.SkillInvocation, brief string) string {
	prefix := brief + "\n\n"
	if invocation != nil {
		invocation.Prompt = prefix + invocation.Prompt
		invocation.PlatformPrompt = prefix + invocation.PlatformPrompt
	}
	return prefix + prompt
}
