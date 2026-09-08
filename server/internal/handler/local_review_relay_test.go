package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewRelayRoutesAndDiscardsCompletedData(t *testing.T) {
	// Given an in-flight request with no database or persistent store.
	var relay localReviewRelay
	command := protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime", TaskID: "task", Action: "read", CommandID: "client-operation"}
	pending, err := relay.enqueue(command)
	if err != nil {
		t.Fatal(err)
	}
	if got := relay.claim("other-workspace", "runtime"); got != nil {
		t.Fatal("cross-workspace claim")
	}
	if got := relay.claim("ws", "other-runtime"); got != nil {
		t.Fatal("cross-runtime claim")
	}
	claimed := relay.claim("ws", "runtime")
	if claimed == nil {
		t.Fatal("missing forwarded request")
	}
	if claimed.CommandID != "client-operation" || claimed.ID == claimed.CommandID {
		t.Fatal("relay confused transport correlation with operation identity")
	}
	result := protocol.LocalReviewResult{ClaimToken: claimed.ClaimToken, Snapshot: []byte(`{"id":"snapshot"}`)}
	if relay.complete("ws", "wrong-runtime", claimed.ID, result) {
		t.Fatal("accepted wrong runtime result")
	}
	wrongClaim := result
	wrongClaim.ClaimToken = "wrong"
	if relay.complete("ws", "runtime", claimed.ID, wrongClaim) {
		t.Fatal("accepted wrong claim")
	}
	// When the owning runtime reports and the requesting client consumes it.
	if !relay.complete("ws", "runtime", claimed.ID, result) {
		t.Fatal("rejected owner result")
	}
	got, err := relay.wait(context.Background(), pending)
	// Then the response arrives and neither request nor diff remains in the relay.
	if err != nil || string(got.Snapshot) != `{"id":"snapshot"}` {
		t.Fatal(got, err)
	}
	if len(relay.pending) != 0 {
		t.Fatal("relay retained completed data")
	}
	if relay.complete("ws", "runtime", claimed.ID, result) {
		t.Fatal("accepted expired response")
	}
}

func TestLocalReviewRelayCancellationDiscardsUnclaimedRequest(t *testing.T) {
	// Given a client disconnect before the runtime has claimed the request.
	var relay localReviewRelay
	pending, err := relay.enqueue(protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When the client wait is cancelled.
	_, err = relay.wait(ctx, pending)
	// Then no offline operation can later be claimed or executed.
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if relay.claim("ws", "runtime") != nil || len(relay.pending) != 0 {
		t.Fatal("cancelled request retained")
	}
}

func TestLocalReviewRelayBoundsPendingRequests(t *testing.T) {
	// Given a full relay with no responding runtime.
	var relay localReviewRelay
	var first *localReviewExchange
	for range localReviewRelayCapacity {
		exchange, err := relay.enqueue(protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime"})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = exchange
		}
	}
	// When another request arrives.
	_, err := relay.enqueue(protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime"})
	// Then memory growth is refused, and claim order remains FIFO.
	if !errors.Is(err, errLocalReviewRelayBusy) {
		t.Fatal(err)
	}
	claimed := relay.claim("ws", "runtime")
	if claimed == nil || claimed.ID != first.command.ID {
		t.Fatal("oldest request not claimed first")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := relay.wait(ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := relay.enqueue(protocol.LocalReviewCommand{}); err != nil {
		t.Fatal("cancel did not free capacity", err)
	}
}
