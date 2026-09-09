package handler

import (
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewRelaySharedByIssueTableSnapshot(t *testing.T) {
	// Given the initialized handler used by both API routes and query snapshots.
	snapshot, tx, err := testHandler.beginIssueTableSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if &snapshot.localReviewRelay.mu != &testHandler.localReviewRelay.mu {
		t.Fatal("handler snapshot copied the relay mutex instead of sharing its lifecycle")
	}

	// When both handler views concurrently enqueue requests for the same runtime.
	var requests sync.WaitGroup
	for i := range 32 {
		requests.Go(func() {
			h := testHandler
			if i%2 == 0 {
				h = snapshot
			}
			if _, err := h.localReviewRelay.enqueue(protocol.LocalReviewCommand{
				WorkspaceID: "snapshot-workspace", RuntimeID: "snapshot-runtime",
			}); err != nil {
				t.Error(err)
			}
		})
	}
	requests.Wait()

	// Then the original handler sees every request once and can complete each one.
	for range 32 {
		command := testHandler.localReviewRelay.claim("snapshot-workspace", "snapshot-runtime")
		if command == nil {
			t.Fatal("request was isolated in the snapshot relay")
		}
		result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken}
		if !snapshot.localReviewRelay.complete(command.WorkspaceID, command.RuntimeID, command.ID, result) {
			t.Fatal("snapshot could not complete original handler claim")
		}
		pending := testHandler.localReviewRelay.pending[command.ID]
		if _, err := testHandler.localReviewRelay.wait(t.Context(), pending); err != nil {
			t.Fatal(err)
		}
	}
	if testHandler.localReviewRelay.claim("snapshot-workspace", "snapshot-runtime") != nil {
		t.Fatal("request was claimed more than once")
	}
}
