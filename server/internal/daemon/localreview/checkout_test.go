package localreview

import (
	"strings"
	"testing"
)

func TestCheckoutBranchExcludesDirtyStateAndAcceptsKnownRemoteRef(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "committed checkout\n")
	run(t, repo, "commit", "-am", "checkout change")
	run(t, repo, "update-ref", "refs/remotes/origin/main", run(t, repo, "rev-parse", "main"))
	write(t, repo, "app.txt", "uncommitted private\n")
	before := run(t, repo, "status", "--porcelain")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCheckout(t.Context(), CheckoutRequest{Path: repo, Scope: "branch", Target: "refs/remotes/origin/main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 1 || version.Header.Dirty || !version.Header.Committed {
		t.Fatalf("wrong committed comparison: %+v", version)
	}
	patch, err := store.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: patch, Page: PatchPageRequest{Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	// The object content distinguishes the committed tree from the dirty checkout.
	content, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "app.txt", Side: "new", Limit: 1000})
	if err != nil || content.Text != "committed checkout\n" {
		t.Fatalf("wrong object content: %+v %v %+v", content, err, page)
	}
	if run(t, repo, "status", "--porcelain") != before {
		t.Fatal("read modified checkout")
	}
	if _, err := store.CaptureCheckout(t.Context(), CheckoutRequest{Path: repo, Scope: "branch", Target: "main~1"}); err == nil {
		t.Fatal("accepted revision expression instead of known ref")
	}
}

func TestCheckoutCommitUsesFirstParentAndSupportsRoot(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "first commit\n")
	run(t, repo, "commit", "-am", "first")
	first := run(t, repo, "rev-parse", "HEAD")
	write(t, repo, "app.txt", "later commit\n")
	run(t, repo, "commit", "-am", "later")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, sha := range []string{first, run(t, repo, "rev-list", "--max-parents=0", "HEAD")} {
		version, err := store.CaptureCheckout(t.Context(), CheckoutRequest{Path: repo, Scope: "commit", Commit: sha})
		if err != nil {
			t.Fatal(err)
		}
		commits, err := version.CommitPage(t.Context(), FilePageRequest{Limit: 100})
		if err != nil || len(commits.Commits) != 1 || commits.Commits[0].SHA != sha || version.Header.Head != sha || !version.Header.CommitOnly {
			t.Fatalf("wrong selected commit: %+v %v", commits, err)
		}
		if sha == first {
			content, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "app.txt", Side: "new", Limit: 1000})
			if err != nil || content.Text != "first commit\n" {
				t.Fatal(content, err)
			}
		}
	}
	run(t, repo, "checkout", "--orphan", "unrelated")
	run(t, repo, "commit", "-am", "unrelated")
	unrelated := run(t, repo, "rev-parse", "HEAD")
	run(t, repo, "checkout", "feature")
	if _, err := store.CaptureCheckout(t.Context(), CheckoutRequest{Path: repo, Scope: "commit", Commit: unrelated}); err == nil || !strings.Contains(err.Error(), "outside checkout history") {
		t.Fatal("accepted unrelated commit", err)
	}
}

func TestCheckoutMergeCommitComparesFirstParent(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "mainline\n")
	run(t, repo, "commit", "-am", "mainline")
	firstParent := run(t, repo, "rev-parse", "HEAD")
	run(t, repo, "checkout", "-b", "side", "main")
	write(t, repo, "side.txt", "side branch\n")
	run(t, repo, "add", "side.txt")
	run(t, repo, "commit", "-m", "side")
	run(t, repo, "checkout", "feature")
	run(t, repo, "merge", "--no-ff", "side", "-m", "merge side")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCheckout(t.Context(), CheckoutRequest{Path: repo, Scope: "commit", Commit: run(t, repo, "rev-parse", "HEAD")})
	if err != nil {
		t.Fatal(err)
	}
	if version.Header.Base != firstParent || len(version.Files) != 1 || version.Files[0].Path != "side.txt" {
		t.Fatalf("merge did not use first parent: %+v", version)
	}
}
