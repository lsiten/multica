package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func verifyIndexReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.checkout, "selected.txt"), []byte("index process fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Action: "index"}
	var state localIndexView
	if err := json.Unmarshal(fixture.run(input)["page"], &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Status.Files) != 1 || state.Status.Files[0].Path != "selected.txt" {
		t.Fatal("index read mixed historical changes")
	}
	input.VersionID, input.IndexID = state.VersionID, state.Status.IndexID
	input.Target, input.Branch, input.Head, input.Paths = state.Status.Branch, state.Status.Branch, state.Status.Head, []string{"selected.txt"}
	verifyRetiredReviewProcess(t, fixture, input)
}
