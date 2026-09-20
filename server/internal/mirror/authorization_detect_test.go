package mirror

import "testing"

func TestDetectAuthorizationPromptRequiresExplicitPhrase(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		{"codex login", "Codex login required. Run /login", true},
		{"auth required", "Authentication required to continue", true},
		{"ordinary 401", "request failed: 401", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, got := DetectAuthorizationPrompt(tc.text)
			if got != tc.want {
				t.Fatalf("detected = %v, want %v", got, tc.want)
			}
		})
	}
}
