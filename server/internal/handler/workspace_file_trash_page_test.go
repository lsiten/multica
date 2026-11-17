package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceTrashRelayShowsOnlyThisNamespacesConfirmedDeletions(t *testing.T) {
	directory, _ := workspaceFileMutationFixture(t)
	finishFileMutation(t, admitFileMutation(t, directory, "private-delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes.md"}))
	alias := workspaceFileRecoveryAlias(t, directory)
	finishFileMutation(t, admitFileMutation(t, alias, "own-delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes.md"}))
	page, err := json.Marshal(protocol.WorkspaceTrashPage{Kind: "workspace_trash", Path: alias.root, Entries: []protocol.WorkspaceTrashEntry{
		{RecoveryID: "private-delete", Path: "private.md", ActorID: "forged"},
		{RecoveryID: "unknown-delete", Path: "unknown.md"},
		{RecoveryID: "own-delete", Path: "forged.md", Directory: true, ActorID: "forged"},
	}, NextOffset: 23, HasMore: true})
	if err != nil {
		t.Fatal(err)
	}
	request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", protocol.LocalReviewCommand{TaskID: alias.binding.TaskID, Path: alias.root, Action: "workspace_trash", Offset: 20, Limit: 3}))
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			command := testHandler.localReviewRelay.claim(testWorkspaceID, alias.binding.RuntimeID)
			if command == nil {
				continue
			}
			testHandler.localReviewRelay.complete(testWorkspaceID, alias.binding.RuntimeID, command.ID, protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: page})
			return
		}
	}()
	var response struct {
		Page protocol.WorkspaceTrashPage `json:"page"`
	}

	testutil.Call(t, testHandler.ForwardLocalReview, request.WithContext(ctx)).Want(http.StatusOK).JSON(&response)

	if len(response.Page.Entries) != 1 {
		t.Fatalf("unowned or unconfirmed recoveries were exposed: %+v", response.Page.Entries)
	}
	entry := response.Page.Entries[0]
	if entry.RecoveryID != "own-delete" || entry.Path != "notes.md" || entry.Directory || entry.ActorID != testUserID {
		t.Fatalf("untrusted runtime metadata replaced the confirmed deletion: %+v", entry)
	}
	if response.Page.NextOffset != 23 || !response.Page.HasMore {
		t.Fatalf("filtering lost the physical scan continuation: %+v", response.Page)
	}
}
