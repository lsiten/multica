package daemon

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentUnadmittedRequestClearsAfterSelectiveEpochAdvance(t *testing.T) {
	c, _, _ := newEnvironmentClientForService(t)
	c.gate <- struct{}{}
	defer func() { <-c.gate }()
	status, err := c.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unsent, err := c.process.Client.Request("environment.bind", status.Fence, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unsent.Deadline = time.Now().Add(time.Minute)
	c.trackPending(environmentPendingOperation{Request: unsent, Scope: environmentMutationScope{Global: true}, ScopeResolved: true})
	c.reconcilePending(t.Context())
	c.mu.Lock()
	retained := len(c.pending) == 1
	c.mu.Unlock()
	if !retained {
		t.Fatal("absence before the admission deadline cleared uncertainty")
	}
	accepted, err := c.process.Client.Request("environment.bind", status.Fence, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.process.Client.Call(t.Context(), accepted); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.consumeLocked(accepted.RequestID)
	err = c.retireReceipts(t.Context())
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.reconcilePending(t.Context())
	c.mu.Lock()
	remaining := len(c.pending)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatal("retired unadmitted request permanently blocks its directory")
	}
	_, err = c.process.Client.Call(t.Context(), unsent)
	var rejection *runtimeproc.Error
	if !errors.As(err, &rejection) || rejection.Code != "retired_request" {
		t.Fatalf("delayed old-epoch write could still be admitted: %v", err)
	}
}
