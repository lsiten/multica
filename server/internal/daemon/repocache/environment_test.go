package repocache

import (
	"reflect"
	"testing"
)

func TestGitEnvironmentExcludesProviderAndAccountCredentials(t *testing.T) {
	safe := []string{"PATH=/fake/bin", "HOME=/owned/home", "SSH_AUTH_SOCK=/owned/ssh", "HTTPS_PROXY=http://local", "GIT_SSH_COMMAND=fake-ssh", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://owned.invalid/.extraheader", "GIT_CONFIG_VALUE_0=Authorization: repo-only", "GIT_CONFIG_GLOBAL=/owned/gitconfig", "GH_CONFIG_DIR=/owned/gh"}
	input := append(append([]string{}, safe...), "OPENAI_API_KEY=private", "ANTHROPIC_API_KEY=private", "MULTICA_TOKEN=private", "GH_TOKEN=private", "GITHUB_TOKEN=private", "GIT_CONFIG_VALUE_BAD=private", "UNRELATED_SECRET=private")
	if got := GitEnvironment(input); !reflect.DeepEqual(got, safe) {
		t.Fatalf("Git environment got keys/values %+v; want %+v", got, safe)
	}
}
