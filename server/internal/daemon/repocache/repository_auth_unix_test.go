//go:build !windows

package repocache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fixtureRepositoryAuth struct{}

func (fixtureRepositoryAuth) GitEnvironment(_ context.Context, workspaceID, url string) (map[string]string, error) {
	if workspaceID != "owned" {
		return nil, errors.New("not authorized")
	}
	switch url {
	case "https://owned.invalid/one":
		return map[string]string{"GH_TOKEN": "repo-one-marker"}, nil
	case "https://owned.invalid/two":
		return map[string]string{"GH_TOKEN": "repo-two-marker"}, nil
	default:
		return nil, errors.New("not authorized")
	}
}

func TestRepositoryAuthScopesFakeGitHelperWithoutChangingLegacyEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "legacy-marker")
	t.Setenv("MULTICA_TOKEN", "account-marker")
	t.Setenv("OPENAI_API_KEY", "provider-marker")
	bin := t.TempDir()
	script := `#!/bin/sh
set -eu
case "$2" in
legacy) [ "${GH_TOKEN-}" = legacy-marker ] ;;
one) [ "${GH_TOKEN-}" = repo-one-marker ]; [ -z "${MULTICA_TOKEN-}" ]; [ -z "${OPENAI_API_KEY-}" ] ;;
two) [ "${GH_TOKEN-}" = repo-two-marker ]; [ -z "${MULTICA_TOKEN-}" ]; [ -z "${OPENAI_API_KEY-}" ] ;;
leak) printf '%s\n' "$GH_TOKEN"; exit 1 ;;
*) exit 88 ;;
esac
printf 'helper-accepted\n'
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := runGitOutputContext(t.Context(), "verify", "legacy"); err != nil || string(output) != "helper-accepted\n" {
		t.Fatalf("legacy env-only Git helper changed: %q %v", output, err)
	}
	cache := NewWithRepositoryAuth(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), fixtureRepositoryAuth{})
	var workers sync.WaitGroup
	for _, repo := range []string{"one", "two"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, release, err := cache.repositoryContext(t.Context(), "owned", "https://owned.invalid/"+repo)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			output, err := runGitOutputContext(ctx, "verify", repo)
			if err != nil || string(output) != "helper-accepted\n" {
				t.Errorf("repo %s environment crossed scope: %q %v", repo, output, err)
			}
		}()
	}
	workers.Wait()
	if err := cache.SyncContext(t.Context(), "owned", []RepoInfo{{URL: "https://unauthorized.invalid/repository"}}); err == nil {
		t.Fatal("unauthorized Git request accepted")
	}
	scoped, release, err := cache.repositoryContext(t.Context(), "owned", "https://owned.invalid/one")
	if err != nil {
		t.Fatal(err)
	}
	output, err := runGitCombinedOutputContext(scoped, "verify", "leak")
	if err == nil || strings.Contains(string(output), "repo-one-marker") || !strings.Contains(string(output), "[redacted git credential]") {
		t.Fatalf("Git output leaked credential: %q %v", output, err)
	}
	release()
	if auth := scoped.Value(repositoryAuthContextKey{}).(map[string]string); len(auth) != 0 {
		t.Fatal("completed Git operation retained credential map")
	}
	if os.Getenv("GH_TOKEN") != "legacy-marker" {
		t.Fatal("repository command changed global Git environment")
	}
}
