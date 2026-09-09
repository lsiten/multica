package daemon

import (
	"encoding/json"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"strings"
	"testing"
)

type processReviewFixture struct {
	taskID, checkout, repo, sourceHead string
	run                                func(protocol.LocalReviewCommand) map[string]json.RawMessage
}

func verifyLegacyReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Target: "main", Action: "read"}
	view := fixture.run(input)
	var snapshot struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(view["snapshot"], &snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(view["snapshot"]), "delivered from runtime") {
		t.Fatal("runtime diff missing")
	}
	input.SnapshotID, input.Action, input.CommandID = snapshot.ID, "approve", "process-approve"
	fixture.run(input)
	input.Action, input.CommandID = "merge", "process-merge"
	view = fixture.run(input)
	var merged string
	if err := json.Unmarshal(view["merged_commit"], &merged); err != nil {
		t.Fatal(err)
	}
	var record struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(view["review"], &record); err != nil {
		t.Fatal(err)
	}
	if record.State != "merged" || merged == "" || worktreeTestGit(t, fixture.repo, "rev-parse", "HEAD") != merged {
		t.Fatal("platform and Git target disagree")
	}
	if worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD") != fixture.sourceHead {
		t.Fatal("source checkout moved")
	}
}

func verifyPagedReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Target: "main", Action: "repositories"}
	view := fixture.run(input)
	if !strings.Contains(string(view["page"]), "repositories") {
		t.Fatal("repository response missing")
	}
	input.Action = "manifest"
	view = fixture.run(input)
	var manifest pagedReviewManifest
	if err := json.Unmarshal(view["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Page.TotalFiles != 1 || len(view["page"]) > 256<<10 {
		t.Fatal("manifest aggregated large patch data")
	}
	input.VersionID, input.SnapshotID = manifest.VersionID, manifest.VersionID
	input.Action = "lease"
	view = fixture.run(input)
	if !strings.Contains(string(view["page"]), "expires_at") {
		t.Fatal("read lease missing")
	}
	input.Action, input.FilePath, input.Limit = "file", "app.txt", 30
	view = fixture.run(input)
	var patch pagedReviewFile
	if err := json.Unmarshal(view["page"], &patch); err != nil {
		t.Fatal(err)
	}
	if patch.Page == nil || len(patch.Page.Lines) != 30 || !patch.Page.HasMore {
		t.Fatal("large patch did not page")
	}
	input.Offset = int(patch.Page.NextLine)
	view = fixture.run(input)
	if err := json.Unmarshal(view["page"], &patch); err != nil {
		t.Fatal(err)
	}
	if patch.Page == nil || patch.Page.NextLine <= int64(input.Offset) {
		t.Fatal("patch cursor did not advance")
	}
	input.Action, input.Side, input.Offset, input.Limit = "content", "new", 0, 8
	view = fixture.run(input)
	if !strings.Contains(string(view["page"]), "delivere") {
		t.Fatal("fixed content page missing")
	}
	input.Action, input.Offset, input.Limit = "commits", 0, 50
	view = fixture.run(input)
	if !strings.Contains(string(view["page"]), fixture.sourceHead) {
		t.Fatal("pinned commits missing")
	}
	input.Action, input.CommandID = "approve", "paged-process-approve"
	view = fixture.run(input)
	if err := json.Unmarshal(view["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Review.State != "approved" {
		t.Fatal("approval was not persisted by runtime")
	}
	input.Action, input.CommandID = "merge", "paged-process-merge"
	view = fixture.run(input)
	if err := json.Unmarshal(view["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	merged := manifest.Review.MergedCommit
	if manifest.Review.State != "merged" || merged == "" || worktreeTestGit(t, fixture.repo, "rev-parse", "HEAD") != merged {
		t.Fatal("paged platform and target Git disagree")
	}
	view = fixture.run(input)
	if err := json.Unmarshal(view["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Review.MergedCommit != merged || worktreeTestGit(t, fixture.repo, "rev-parse", "HEAD") != merged {
		t.Fatal("merge redelivery changed the result")
	}
	if worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD") != fixture.sourceHead {
		t.Fatal("source checkout moved")
	}
}
