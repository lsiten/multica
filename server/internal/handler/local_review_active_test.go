package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestClaimedReviewBecomesInactiveAfterReaderDisconnect(t *testing.T) {
	var relay localReviewRelay
	exchange, err := relay.enqueue(protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime", Action: "file"})
	if err != nil {
		t.Fatal(err)
	}
	claim := relay.claim("ws", "runtime")
	if claim == nil {
		t.Fatal("missing claim")
	}
	if !relay.active(*claim) {
		t.Fatal("claimed read was not active")
	}
	for _, foreign := range []protocol.LocalReviewCommand{
		{ID: claim.ID, WorkspaceID: "other", RuntimeID: claim.RuntimeID, ClaimToken: claim.ClaimToken},
		{ID: claim.ID, WorkspaceID: claim.WorkspaceID, RuntimeID: "other", ClaimToken: claim.ClaimToken},
		{ID: claim.ID, WorkspaceID: claim.WorkspaceID, RuntimeID: claim.RuntimeID, ClaimToken: "wrong"},
	} {
		if relay.active(foreign) {
			t.Fatal("foreign claim observed active read")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := relay.wait(ctx, exchange); err == nil {
		t.Fatal("expected cancellation")
	}
	if relay.active(*claim) {
		t.Fatal("disconnected read remained active")
	}
}
