package localreview

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexStatusSeparatesCommittedStagedAndWorkingChanges(t *testing.T) {
	repo := repository(t)
	write(t, repo, "committed.txt", "already committed\n")
	run(t, repo, "add", "committed.txt")
	run(t, repo, "commit", "-m", "branch change")
	write(t, repo, "app.txt", "staged\n")
	run(t, repo, "add", "app.txt")
	write(t, repo, "app.txt", "newer working content\n")
	write(t, repo, "new file\nname.txt", "untracked\n")
	indexPath := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "feature" || len(status.Files) != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}
	for _, file := range status.Files {
		switch file.Path {
		case "app.txt":
			if !file.Staged || !file.Unstaged || file.Untracked {
				t.Fatal("partial staging lost", file)
			}
		case "new file\nname.txt":
			if file.Staged || !file.Unstaged || !file.Untracked {
				t.Fatal("untracked path lost", file)
			}
		default:
			t.Fatal("historical branch diff entered staging list", file)
		}
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("status inspection modified the index")
	}
}

func TestIndexStatusMarksConflictsAndNestedDirectories(t *testing.T) {
	files, err := parseIndexFiles("UU conflict.txt\x00?? nested/\x00")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !files[0].Conflicted || files[1].Path != "nested" || !files[1].Unsupported {
		t.Fatalf("unsafe staging candidates were not identified: %+v", files)
	}
}

func TestIndexStatusRejectsMalformedPathsAndRenameRecords(t *testing.T) {
	for _, raw := range []string{"M app.txt\x00", " M ../outside\x00", "R  destination\x00", " M missing-terminator"} {
		if _, err := parseIndexFiles(raw); !errors.Is(err, ErrInvalidIndexStatus) {
			t.Fatalf("accepted malformed status %q: %v", raw, err)
		}
	}
}

func TestIndexStatusPreservesRenameSourceAndDestination(t *testing.T) {
	repo := repository(t)
	run(t, repo, "mv", "app.txt", "renamed file.txt")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Files) != 1 || status.Files[0].Path != "renamed file.txt" || status.Files[0].OldPath != "app.txt" || !status.Files[0].Staged {
		t.Fatalf("rename identity lost: %+v", status)
	}
}
