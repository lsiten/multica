package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFamilyRedispatchRefreshesCursorPrivateConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, reuse := range []bool{false, true} {
		name := "prepare"
		if reuse {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask, Provider: "cursor", IsolateLocalContext: true, McpConfig: []byte(`{"mcpServers":{"fake":{"url":"https://example.invalid/mcp","headers":{"Authorization":"old-token"}}}}`), Task: TaskContextForEnv{IssueID: "root", AgentID: "agent", CodeWorkspaceIssueID: "root"}}
			first, err := Prepare(params, testLogger())
			if err != nil {
				t.Fatal(err)
			}
			protected := []string{filepath.Join(first.WorkDir, "valuable.txt"), filepath.Join(first.CursorMcpPluginDir, "user-note.txt"), filepath.Join(params.WorkspacesRoot, "other-run", "cursor-mcp-plugin", "mcp.json")}
			for _, path := range protected {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			first.ReleaseLock()
			params.McpConfig = []byte(`{"mcpServers":{"fake":{"url":"https://example.invalid/mcp","headers":{"Authorization":"new-token"}}}}`)
			var second *Environment
			if reuse {
				second = ReusePrivate(ReuseParams{Provider: "cursor", IsolateLocalContext: true, McpConfig: params.McpConfig, Task: params.Task}, first, testLogger())
				if second == nil {
					t.Fatal("same run private reuse failed")
				}
			} else {
				second, err = Prepare(params, testLogger())
				if err != nil {
					t.Fatalf("same task UUID recovery: %v", err)
				}
			}
			defer second.ReleaseLock()
			data, err := os.ReadFile(filepath.Join(second.CursorMcpPluginDir, "mcp.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "new-token") || strings.Contains(string(data), "old-token") {
				t.Fatalf("stale credentials: %s", data)
			}
			for _, path := range protected {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("protected file changed: %s %v", path, err)
				}
			}
		})
	}
}

func TestPrivateSidecarRecoveryDoesNotFollowLinksIntoSharedCode(t *testing.T) {
	root := t.TempDir()
	code := filepath.Join(root, "workdir")
	private := filepath.Join(root, "cursor-mcp-plugin")
	if err := os.MkdirAll(code, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	valuable := filepath.Join(code, "valuable.txt")
	if err := os.WriteFile(valuable, []byte("shared code"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(code, filepath.Join(private, "linked")); err != nil {
		t.Fatal(err)
	}
	manifest := &sidecarManifest{Files: []string{filepath.Join(private, "linked", "valuable.txt")}}
	if err := writeSidecarManifest(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRunPrivateSidecars(root); err == nil {
		t.Fatal("cleanup followed a private namespace escape")
	}
	data, err := os.ReadFile(valuable)
	if err != nil || string(data) != "shared code" {
		t.Fatalf("shared code changed: %q %v", data, err)
	}
}

func TestFamilyRedispatchRecoversAfterCursorPrivatePreparationFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, reuse := range []bool{false, true} {
		name := "prepare"
		if reuse {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask, Provider: "cursor", IsolateLocalContext: true, CursorMcpAuthSource: filepath.Join(t.TempDir(), "missing-auth.json"), McpConfig: []byte(`{"mcpServers":{"fake":{"command":"test-only"}}}`), Task: TaskContextForEnv{IssueID: "root", AgentID: "agent", CodeWorkspaceIssueID: "root"}}
			physical, err := PhysicalPreparationInput(params)
			if err != nil {
				t.Fatal(err)
			}
			env, err := PreparePhysical(physical, testLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer env.ReleaseLock()
			protected := filepath.Join(env.WorkDir, "valuable.txt")
			if err := os.WriteFile(protected, []byte("shared unfinished work"), 0600); err != nil {
				t.Fatal(err)
			}
			if reuse {
				if got := ReusePrivate(ReuseParams{Provider: "cursor", IsolateLocalContext: true, McpConfig: params.McpConfig, CursorMcpAuthSource: params.CursorMcpAuthSource, Task: params.Task}, env, testLogger()); got != nil {
					t.Fatal("invalid auth was accepted")
				}
			} else {
				if _, err := PreparePrivate(params, env, testLogger()); err == nil {
					t.Fatal("invalid auth was accepted")
				}
			}
			params.CursorMcpAuthSource = ""
			if reuse {
				if got := ReusePrivate(ReuseParams{Provider: "cursor", IsolateLocalContext: true, McpConfig: params.McpConfig, Task: params.Task}, env, testLogger()); got == nil {
					t.Fatal("corrected same-run reuse failed")
				}
			} else {
				env.ReleaseLock()
				recovered, err := Prepare(params, testLogger())
				if err != nil {
					t.Fatalf("corrected same-UUID prepare failed: %v", err)
				}
				defer recovered.ReleaseLock()
			}
			data, err := os.ReadFile(protected)
			if err != nil || string(data) != "shared unfinished work" {
				t.Fatalf("shared work changed: %q %v", data, err)
			}
		})
	}
}

func TestPrivateCleanupPreservesUnrecordedInternalSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "cursor-mcp-plugin")
	userDir := filepath.Join(private, "user-notes")
	if err := os.MkdirAll(userDir, 0700); err != nil {
		t.Fatal(err)
	}
	valuable := filepath.Join(userDir, "valuable.txt")
	if err := os.WriteFile(valuable, []byte("unrecorded user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("user-notes", filepath.Join(private, "recorded-dir")); err != nil {
		t.Fatal(err)
	}
	manifest := &sidecarManifest{Files: []string{filepath.Join(private, "recorded-dir", "valuable.txt")}}
	if err := writeSidecarManifest(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRunPrivateSidecars(root); err == nil {
		t.Fatal("changed ancestor symlink was accepted")
	}
	if data, err := os.ReadFile(valuable); err != nil || string(data) != "unrecorded user file" {
		t.Fatalf("cleanup followed changed ancestor symlink and deleted unrecorded file: %q %v", data, err)
	}
}

func TestFamilyRedispatchSurvivesUnrecordedCursorPluginGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, reuse := range []bool{false, true} {
		name := "prepare"
		if reuse {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask, Provider: "cursor", IsolateLocalContext: true, McpConfig: []byte(`{"mcpServers":{"fake":{"command":"test-only","env":{"TOKEN":"old"}}}}`), Task: TaskContextForEnv{IssueID: "root", AgentID: "agent", CodeWorkspaceIssueID: "root"}}
			first, err := Prepare(params, testLogger())
			if err != nil {
				t.Fatal(err)
			}
			oldPlugin := first.CursorMcpPluginDir
			// Model a hard stop after files are written but before the ownership manifest is durable.
			if err := os.Remove(filepath.Join(first.RootDir, sidecarManifestFile)); err != nil {
				t.Fatal(err)
			}
			protected := []string{filepath.Join(first.WorkDir, "valuable.txt"), filepath.Join(oldPlugin, "user-note.txt"), filepath.Join(first.RootDir, "cursor-mcp-plugin", ".cursor-plugin", "plugin.json"), filepath.Join(params.WorkspacesRoot, "other-run", "cursor-mcp-plugin", "mcp.json")}
			for _, path := range protected {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			first.ReleaseLock()
			params.McpConfig = []byte(`{"mcpServers":{"fake":{"command":"test-only","env":{"TOKEN":"new"}}}}`)
			var second *Environment
			if reuse {
				second = ReusePrivate(ReuseParams{Provider: "cursor", IsolateLocalContext: true, McpConfig: params.McpConfig, Task: params.Task}, first, testLogger())
				if second == nil {
					t.Fatal("unrecorded plugin blocks reuse")
				}
			} else {
				second, err = Prepare(params, testLogger())
				if err != nil {
					t.Fatalf("unrecorded plugin blocks preparation: %v", err)
				}
			}
			defer second.ReleaseLock()
			if second.CursorMcpPluginDir == oldPlugin {
				t.Fatal("reclaimed unrecorded plugin generation")
			}
			data, err := os.ReadFile(filepath.Join(second.CursorMcpPluginDir, "mcp.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"new"`) || strings.Contains(string(data), `"old"`) {
				t.Fatalf("wrong fresh plugin config: %s", data)
			}
			for _, path := range protected {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("unrecorded content changed: %s %v", path, err)
				}
			}
		})
	}
}

func TestFamilyRedispatchUsesFreshContextGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, reuse := range []bool{false, true} {
		for _, recorded := range []bool{false, true} {
			name := fmt.Sprintf("reuse=%t/recorded=%t", reuse, recorded)
			t.Run(name, func(t *testing.T) {
				params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask, Provider: "claude", IsolateLocalContext: true, Task: TaskContextForEnv{IssueID: "root", AgentID: "agent", CodeWorkspaceIssueID: "root", AgentSkills: []SkillContextForEnv{{Name: "Old capability", Content: "old capability"}}}}
				first, err := Prepare(params, testLogger())
				if err != nil {
					t.Fatal(err)
				}
				oldContext := first.ContextDir
				if !recorded {
					if err := os.Remove(filepath.Join(first.RootDir, sidecarManifestFile)); err != nil {
						t.Fatal(err)
					}
				}
				first.ReleaseLock()
				params.Task.AgentSkills = []SkillContextForEnv{{Name: "Current capability", Content: "current capability"}}
				var second *Environment
				if reuse {
					second = ReusePrivate(ReuseParams{Provider: params.Provider, IsolateLocalContext: true, Task: params.Task}, first, testLogger())
					if second == nil {
						t.Fatal("context recovery refused reuse")
					}
				} else {
					second, err = Prepare(params, testLogger())
					if err != nil {
						t.Fatal(err)
					}
				}
				defer second.ReleaseLock()
				if second.ContextDir == oldContext {
					t.Fatal("retry reused the prior context generation")
				}
				if _, err := os.Stat(filepath.Join(skillsDirPath(second.ContextDir, "claude"), "old-capability", "SKILL.md")); !os.IsNotExist(err) {
					t.Fatalf("stale capability in active context: %v", err)
				}
				if _, err := os.Stat(filepath.Join(skillsDirPath(second.ContextDir, "claude"), "current-capability", "SKILL.md")); err != nil {
					t.Fatalf("current capability missing: %v", err)
				}
				_, oldErr := os.Stat(oldContext)
				if recorded && !os.IsNotExist(oldErr) {
					t.Fatalf("manifest-owned old generation was not reclaimed: %v", oldErr)
				}
				if !recorded && oldErr != nil {
					t.Fatalf("unknown prior generation was removed: %v", oldErr)
				}
			})
		}
	}
}
