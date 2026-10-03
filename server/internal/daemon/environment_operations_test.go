package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentOperationRestartKeepsPartialResultsAndNeverRepeatsMutation(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []protocol.EnvironmentSelection{{EnvironmentID: strings.Repeat("b", 64), Revision: strings.Repeat("c", 64)}, {EnvironmentID: strings.Repeat("d", 64), Revision: strings.Repeat("e", 64)}}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	now := time.Now().UTC()
	record := environmentOperationRecord{EnvironmentOperationStatus: protocol.EnvironmentOperationStatus{ID: request.ID, Action: request.Action, Status: "running", WorkspaceID: scope.WorkspaceID, RuntimeID: scope.RuntimeID, DaemonID: d.cfg.DaemonID, Profile: d.cfg.Profile, StartedAt: now, UpdatedAt: now, Completed: 1, Total: 2, Results: []json.RawMessage{json.RawMessage(`{"environment_id":"` + strings.Repeat("b", 64) + `","removed_count":1}`)}}, BackendURL: d.cfg.ServerBaseURL, InputHash: hex.EncodeToString(digest[:])}
	if err := d.writeEnvironmentOperation(scope, record); err != nil {
		t.Fatal(err)
	}
	status, err := d.environmentOperationStatus(scope, request.ID, false)
	if err != nil || status.Status != "interrupted" || status.Completed != 1 || len(status.Results) != 1 {
		t.Fatalf("partial recovery receipt lost: %+v %v", status, err)
	}
	replay, err := d.startEnvironmentOperation(scope, request)
	if err != nil || replay.Status != "interrupted" || replay.Completed != 1 {
		t.Fatalf("interrupted operation replayed: %+v %v", replay, err)
	}
	if len(d.environmentOperations) != 0 {
		t.Fatal("restart launched a second mutation")
	}
	list, err := d.listEnvironmentOperations(scope)
	if err != nil || len(list) != 1 || list[0].Status != "interrupted" {
		t.Fatalf("restart receipt not discoverable: %+v %v", list, err)
	}
}

func TestEnvironmentOperationCancelDuringDiscoveryRetainsSourceAndReceipt(t *testing.T) {
	entered := make(chan struct{}, 1)
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	writeLifecycleFile(t, cache, "retain cache")
	owner, err := d.gcTaskDirOwner(root)
	if err != nil {
		t.Fatal(err)
	}
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []protocol.EnvironmentSelection{{EnvironmentID: d.managedEnvironmentID(root, "ws1", owner.TaskID), Revision: strings.Repeat("b", 64)}}}
	if _, err := d.startEnvironmentOperation(scope, request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("discovery did not start")
	}
	if _, err := d.environmentOperationStatus(scope, request.ID, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { d.environmentOperationWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("cancelled discovery remained running")
	}
	status, err := d.environmentOperationStatus(scope, request.ID, false)
	if err != nil || status.Status != "cancelled" || status.Completed != 0 || len(status.Results) != 0 {
		t.Fatalf("wrong cancellation receipt: %+v %v", status, err)
	}
	if data, err := os.ReadFile(cache); err != nil || string(data) != "retain cache" {
		t.Fatal("unprocessed cache removed")
	}
}
