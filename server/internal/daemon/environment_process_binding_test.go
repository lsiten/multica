package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentUnchangedBindingDoesNotBlockIndependentRecovery(t *testing.T) {
	var bindings atomic.Int32
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			bindings.Add(1)
			return json.RawMessage(`{}`), nil
		}
	})
	c.daemon = &Daemon{cfg: Config{WorkspacesRoot: t.TempDir()}, workspaces: map[string]*workspaceState{
		"ws-a": {runtimeIDs: []string{"rt-b", "rt-a"}, allowedRepoURLs: map[string]struct{}{"repo-b": {}, "repo-a": {}}},
		"ws-b": {runtimeIDs: []string{"rt-c"}, taskRepoURLs: map[string]struct{}{"repo-d": {}, "repo-c": {}}},
	}}
	c.facts = &environmentFactCallback{address: "http://127.0.0.1:1"}
	if err := c.sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := c.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unresolved, err := c.process.Client.Request("physical.prepare", status.Fence, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unresolved.Deadline = time.Now().Add(time.Minute)
	c.trackPending(environmentPendingOperation{Request: unresolved, Scope: environmentMutationScope{WorkspaceID: "ws-a", Paths: []string{"/workspace/a"}}, ScopeResolved: true, Cause: errors.New("outcome pending")})
	for range 32 {
		if err := c.sync(t.Context()); err != nil {
			t.Fatalf("unchanged binding incorrectly blocked other task startup: %v", err)
		}
	}
	if bindings.Load() != 1 {
		t.Fatalf("unchanged roster performed %d binding mutations", bindings.Load())
	}
	c.daemon.mu.Lock()
	c.daemon.workspaces["ws-a"].allowedRepoURLs["new-repository"] = struct{}{}
	c.daemon.mu.Unlock()
	var uncertain *environmentUncertainOperation
	if err := c.sync(t.Context()); !errors.As(err, &uncertain) {
		t.Fatalf("changed global binding bypassed uncertainty protection: %v", err)
	}
}
