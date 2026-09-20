package mirror

import "strings"

// DetectAuthorizationPrompt recognises explicit authentication prompts emitted
// by common local CLIs. It deliberately requires a prompt-shaped phrase and
// never treats an arbitrary 401/error as a request that can be approved.
func DetectAuthorizationPrompt(output string) (kind, title, message string, ok bool) {
	text := strings.ToLower(strings.TrimSpace(output))
	if text == "" || len(text) > 16*1024 {
		return "", "", "", false
	}
	cli := []string{
		"run /login", "please log in", "please login", "authentication required",
		"authorization required", "sign in to codex", "codex login",
	}
	for _, phrase := range cli {
		if strings.Contains(text, phrase) {
			return "cli", "需要命令行授权", "运行时命令行需要登录或授权，请在此确认后继续。", true
		}
	}
	return "", "", "", false
}
