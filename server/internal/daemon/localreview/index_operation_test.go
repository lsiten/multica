package localreview

import "testing"

func TestIndexOperationReplayDoesNotStageLaterWorkingEdits(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "reviewed\n")
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := IndexOperationRequest{Path: version.Header.Repository, CommandID: "stage-once", ActorID: "owner", Action: "stage", VersionID: id, IndexID: status.IndexID, Branch: "feature", Head: status.Head, Paths: []string{"app.txt"}}
	first, err := store.ExecuteIndexOperation(t.Context(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "app.txt", "later must remain unstaged\n")
	again, err := store.ExecuteIndexOperation(t.Context(), root, request)
	if err != nil || again.IndexID != first.IndexID {
		t.Fatal("lost operation result", err)
	}
	if got := run(t, repo, "show", ":app.txt"); got != "reviewed" {
		t.Fatal("replay staged later content", got)
	}
}

func TestIndexOperationRecoversCommitWithoutCreatingAnotherCommit(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged\n")
	run(t, repo, "add", "app.txt")
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := IndexOperationRequest{Path: version.Header.Repository, CommandID: "commit-once", ActorID: "owner", Action: "commit", VersionID: id, IndexID: status.IndexID, Branch: "feature", Head: status.Head, Message: "staged commit"}
	commit, err := CommitIndexPrepared(t.Context(), IndexCommitRequest{Path: request.Path, Branch: request.Branch, Head: request.Head, IndexID: request.IndexID, Message: request.Message}, func(prepared PreparedIndexCommit) error {
		return PrepareIndexReceipt(root, request, IndexOperationResult{Commit: &prepared})
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate successful Git publication with a lost completion write, then
	// another ordinary commit before the caller retries the original request.
	run(t, repo, "commit", "--allow-empty", "-m", "later")
	later := run(t, repo, "rev-parse", "HEAD")
	result, err := store.ExecuteIndexOperation(t.Context(), root, request)
	if err != nil || result.Commit == nil || result.Commit.Commit != commit {
		t.Fatal("commit recovery failed", err)
	}
	if run(t, repo, "rev-parse", "HEAD") != later {
		t.Fatal("recovery changed later history")
	}
	receipt, exists, err := LoadIndexReceipt(root, request)
	if err != nil || !exists || receipt.State != "completed" {
		t.Fatal("recovery did not complete receipt", err)
	}
}
