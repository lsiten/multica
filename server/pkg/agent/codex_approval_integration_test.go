//go:build agentintegration

package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodexRealApprovalDecline exercises an installed app-server and its actual
// approval RPC. The harmless write is always declined and must not reach disk.
func TestCodexRealApprovalDecline(t *testing.T) {
	runCodexRealApproval(t, false)
}

func TestCodexRealApprovalAccept(t *testing.T) {
	runCodexRealApproval(t, true)
}

func runCodexRealApproval(t *testing.T, approve bool) {
	t.Helper()
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("authorized smoke requires Codex on PATH")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "approval-marker")
	peerApproval := realApprovalPeer(t, approve)
	reviewed := make(chan struct{}, 1)
	backend := &codexBackend{cfg: Config{
		ExecutablePath: path,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestApproval: func(ctx context.Context, request ApprovalRequest) (bool, error) {
			var params struct {
				Command string `json:"command"`
				Cwd     string `json:"cwd"`
			}
			if request.Method != "item/commandExecution/requestApproval" || json.Unmarshal(request.Params, &params) != nil {
				return false, nil
			}
			command := strings.TrimSpace(params.Command)
			cwd, cwdErr := filepath.EvalSymlinks(params.Cwd)
			expectedCwd, expectedErr := filepath.EvalSymlinks(directory)
			expectedCommand := command == "mkdir approval-marker" || command == "/bin/zsh -lc 'mkdir approval-marker'" || command == "/bin/bash -lc 'mkdir approval-marker'"
			if expectedCommand && cwdErr == nil && expectedErr == nil && cwd == expectedCwd {
				decision, err := peerApproval(ctx, request)
				if err != nil || decision != approve {
					t.Errorf("real peer approval decision=%v error=%v", decision, err)
					return false, err
				}
				select {
				case reviewed <- struct{}{}:
				default:
				}
				return decision, nil
			}
			return false, nil
		},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	session, err := backend.executeOnce(ctx,
		"Approval transport test. Use your shell tool exactly once to run `mkdir approval-marker` in the current directory, requesting elevated permission and explaining that this creates only a disposable test directory. If rejected, do not retry, use another tool, or change the command; reply declined and stop.",
		ExecOptions{Cwd: directory, Timeout: 85 * time.Second, ThinkingLevel: "low", ExtraArgs: []string{"-c", `approval_policy="on-request"`, "-c", `sandbox_mode="read-only"`}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case _, ok := <-session.Messages:
			if !ok {
				session.Messages = nil
			}
		case result := <-session.Result:
			select {
			case <-reviewed:
			default:
				t.Fatalf("real app-server did not deliver the expected approval RPC: status=%s error=%s", result.Status, result.Error)
			}
			info, err := os.Stat(marker)
			if approve {
				if err != nil || !info.IsDir() {
					t.Fatal("approved command did not create the test directory")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("declined command modified the filesystem")
			}
			return
		case <-ctx.Done():
			t.Fatal("real approval smoke timed out")
		}
	}
}
