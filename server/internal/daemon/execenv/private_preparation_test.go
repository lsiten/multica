package execenv

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPrivatePreparationHelperDoesNotReacquireOrResetPhysicalRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	physical, err := PreparePhysical(PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.ReleaseLock()
	sentinel := filepath.Join(physical.WorkDir, "user-owned.txt")
	if err = os.WriteFile(sentinel, []byte("keep exact content"), 0600); err != nil {
		t.Fatal(err)
	}
	private, err := PreparePrivateIsolated(context.Background(), preparationHelperTestCommand(), PrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task", Provider: "claude", IsolateLocalContext: true}, physical, logger)
	if err != nil {
		t.Fatal(err)
	}
	if private == nil || private.RootDir != physical.RootDir || private.WorkDir != physical.WorkDir {
		t.Fatalf("private preparation changed physical selection: %+v", private)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep exact content" {
		t.Fatalf("private helper reset physical work: %q %v", data, err)
	}
	if second, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task"}); err == nil {
		second.Release()
		t.Fatal("private helper completion dropped actual owner's kernel claim")
	}
	if _, err = os.Stat(private.MulticaConfigRoot); err != nil {
		t.Fatal("private helper did not create task-local configuration")
	}
}

func TestPrivatePreparationEnvironmentExcludesAmbientCredentials(t *testing.T) {
	got := privatePreparationEnvironment([]string{"HOME=/owned/home", "PATH=/owned/bin", "CODEX_HOME=/owned/codex", "OPENCLAW_CONFIG_PATH=/owned/openclaw.json", "MULTICA_TOKEN=account", "OPENAI_API_KEY=ambient-provider", "UNRELATED_SECRET=private"})
	want := []string{"HOME=/owned/home", "PATH=/owned/bin", "CODEX_HOME=/owned/codex", "OPENCLAW_CONFIG_PATH=/owned/openclaw.json"}
	if !slices.Equal(got, want) {
		t.Fatalf("private helper inherited ambient credentials: %v", got)
	}
}

func TestPrivateOpenclawHelperPreservesArbitraryProviderVariableWithoutAccountToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("F1_ARBITRARY_PROVIDER_VARIABLE", "resolved-fixture-agent")
	t.Setenv("MULTICA_TOKEN", "account-marker-must-not-cross")
	root := t.TempDir()
	config := filepath.Join(home, "openclaw.json")
	if err := os.WriteFile(config, []byte(`{"agents":{"list":[{"id":"${F1_ARBITRARY_PROVIDER_VARIABLE}"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "fake-openclaw")
	script := "#!/bin/sh\nset -eu\n[ \"${F1_ARBITRARY_PROVIDER_VARIABLE-}\" = resolved-fixture-agent ] || exit 81\n[ -z \"${MULTICA_TOKEN-}\" ] || exit 82\ncase \"$2\" in\nfile) printf '%s\\n' '" + config + "' ;;\nget) printf '[{\"id\":\"%s\"}]\\n' \"$F1_ARBITRARY_PROVIDER_VARIABLE\" ;;\n*) exit 83 ;;\nesac\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	physical, err := PreparePhysical(PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "private-openclaw"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.ReleaseLock()
	privateEnv := TaskPrivatePreparationEnvironment("openclaw", os.Environ(), nil)
	if _, exists := privateEnv["MULTICA_TOKEN"]; exists {
		t.Fatal("account credential entered private helper input")
	}
	private, err := PreparePrivateIsolated(context.Background(), preparationHelperTestCommand(), PrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "private-openclaw", Provider: "openclaw", OpenclawBin: executable, PrivateEnvironment: privateEnv}, physical, logger)
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := os.ReadFile(private.OpenclawConfigPath)
	if err != nil || !strings.Contains(string(wrapper), "resolved-fixture-agent") {
		t.Fatalf("arbitrary provider variable was not resolved by fake CLI: %s %v", wrapper, err)
	}
}
