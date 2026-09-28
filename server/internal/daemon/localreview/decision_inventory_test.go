package localreview

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestDecisionInventoryRejectsChangedApprovedVersion(t *testing.T) {
	for _, change := range []string{"none", "source", "target", "dirty", "missing_actor"} {
		t.Run(change, func(t *testing.T) {
			repo := repository(t)
			write(t, repo, "app.txt", "reviewed\n")
			run(t, repo, "commit", "-am", "delivery")
			root := t.TempDir()
			store, err := OpenBlobStore(root, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.SaveVersion(t.Context(), version)
			if err != nil {
				t.Fatal(err)
			}
			record := Record{State: "approved", SnapshotID: id, VersionID: id, SourceHead: version.Header.Head, Events: []Event{{Kind: "approve", SnapshotID: id, ActorID: "reviewer"}}}
			if change == "missing_actor" {
				record.Events[0].ActorID = ""
			}
			if err := SaveRecord(root, RecordKey(Snapshot{Path: version.Header.Repository, Target: "main"}), record); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source":
				run(t, repo, "commit", "--allow-empty", "-m", "new delivery")
			case "target":
				run(t, repo, "update-ref", "refs/heads/main", version.Header.Head)
			case "dirty":
				write(t, repo, "app.txt", "unreviewed\n")
			}

			decision, err := InspectDecision(t.Context(), root, repo)

			if err != nil {
				t.Fatal(err)
			}
			want := protocol.WorktreeReview
			if change == "none" {
				want = protocol.WorktreeMerge
			}
			if decision.NextAction != want || decision.Target != "main" {
				t.Fatalf("decision=%+v, want %s", decision, want)
			}
		})
	}
}
