package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func verifySelectedReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.checkout, "unselected.txt"), []byte("must stay on source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, fixture.checkout, "add", "unselected.txt")
	worktreeTestGit(t, fixture.checkout, "commit", "-m", "unselected source change")
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Target: "main", Action: "manifest"}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(fixture.run(input)["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	input.VersionID, input.SnapshotID = manifest.VersionID, manifest.VersionID
	input.Paths, input.Message = []string{"app.txt"}, "apply only chosen file"
	verifyRetiredReviewProcess(t, fixture, input)
}
