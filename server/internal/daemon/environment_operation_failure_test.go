package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentOperationPreservesChildFailureAndPartialResults(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	var calls atomic.Int32
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = append(config.Capabilities, "environment.operation")
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			if calls.Add(1) == 1 {
				return json.RawMessage(`{"removed_count":1}`), nil
			}
			return nil, &runtimeproc.Error{Code: "environment_failed", Message: "unresolved request=original-write; protected directory=/fixture/shared"}
		}
	})
	d.environmentProcess = c
	d.environmentProcessActive.Store(true)
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache"}
	var caches []string
	for _, task := range []string{"first", "second", "third"} {
		root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", task, nil)
		cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
		writeLifecycleFile(t, cache, "retain in control parent")
		caches = append(caches, cache)
		request.Selections = append(request.Selections, protocol.EnvironmentSelection{EnvironmentID: d.managedEnvironmentID(root, "ws1", task), Revision: strings.Repeat("b", 64)})
	}
	if _, err := d.startEnvironmentOperation(scope, request); err != nil {
		t.Fatal(err)
	}
	d.environmentOperationWorkers.Wait()
	status, err := d.environmentOperationStatus(scope, request.ID, false)
	if err != nil || status.Status != "failed" || !strings.Contains(status.Error, "request=original-write") || !strings.Contains(status.Error, "/fixture/shared") {
		t.Fatalf("child failure lost: %+v %v", status, err)
	}
	if status.Completed != 1 || len(status.Results) != 1 || calls.Load() != 2 {
		t.Fatalf("batch did not retain partial progress and stop: %+v calls=%d", status, calls.Load())
	}
	for _, cache := range caches {
		if _, err := os.Stat(cache); err != nil {
			t.Fatalf("parent replayed physical mutation: %v", err)
		}
	}
	if _, err := d.startEnvironmentOperation(scope, request); err != nil || calls.Load() != 2 {
		t.Fatalf("failed operation replayed: calls=%d err=%v", calls.Load(), err)
	}
}
