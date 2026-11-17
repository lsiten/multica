package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestUnknownFileEffectsProtectTheirDirectoryAndAllowIndependentWork(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.environmentServiceOwner = true
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.localPathLocks = NewLocalPathLocker()
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	path := filepath.Join(root, "files")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	command := protocol.LocalReviewCommand{ID: "create-once", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, Action: "file_operation", FileOperation: &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "file.md", Content: "original"}}
	if result := d.runRemoteReview(t.Context(), command); result.Error != "" {
		t.Fatal(result.Error)
	}
	journals, err := filepath.Glob(filepath.Join(d.cfg.WorkspacesRoot, ".workspace-file-operations", "*", "file-operations", "*.json"))
	if err != nil || len(journals) != 1 {
		t.Fatalf("fixture journal: %v, error: %v", journals, err)
	}
	data, err := os.ReadFile(journals[0])
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["state"] = json.RawMessage(`"prepared"`)
	data, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journals[0], data, 0o600); err != nil {
		t.Fatal(err)
	}

	owner := &environmentProcessService{daemon: d}
	var handled atomic.Int32
	client, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			handled.Add(1)
			var input protocol.LocalReviewCommand
			if err := json.Unmarshal(request.Payload, &input); err != nil {
				return environmentOperationResult(nil, err)
			}
			return owner.reviewRunResult(ctx, request, input)
		}
		config.ReadHandler = owner.mutationScopeRead
	})
	_, err = client.routeReviewToChild(t.Context(), command)
	var uncertain *environmentUncertainOperation
	if !errors.As(err, &uncertain) {
		t.Fatalf("unknown file effects lost protection: %v", err)
	}

	second := command
	second.ID = "another-request"
	second.FileOperation = &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "blocked.md"}
	if _, err := client.routeReviewToChild(t.Context(), second); !errors.As(err, &uncertain) {
		t.Fatalf("overlapping operation escaped protection: %v", err)
	}
	if handled.Load() != 1 {
		t.Fatal("overlapping mutation reached the physical handler")
	}
	otherRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task2", nil)
	otherPath := filepath.Join(otherRoot, "files")
	if err := os.Mkdir(otherPath, 0o700); err != nil {
		t.Fatal(err)
	}
	second.ID, second.TaskID, second.Path = "independent", "task2", otherPath
	result, err := client.routeReviewToChild(t.Context(), second)
	if err != nil || result.Error != "" {
		t.Fatalf("independent work blocked: %+v, error: %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(path, "blocked.md")); !os.IsNotExist(err) {
		t.Fatalf("protected directory changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherPath, "blocked.md")); err != nil {
		t.Fatalf("independent file missing: %v", err)
	}
}
