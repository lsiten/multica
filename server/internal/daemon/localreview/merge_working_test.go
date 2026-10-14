package localreview

import (
	"context"
	"os"
	"testing"
)

func TestMergeWorkingFileUnchangedBase(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	writeRootFile(t, root, "notes.txt", "base\n", 0600)
	// current == base (fast path): the edited content becomes the file.
	res, err := MergeWorkingFile(context.Background(), root, MergeWorkingFileRequest{
		FilePath: "notes.txt",
		Base:     []byte("base\n"),
		New:      []byte("base-edited\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Merged || res.Conflict {
		t.Fatalf("expected clean merge, got %+v", res)
	}
	if got := readRootFile(t, root, "notes.txt"); got != "base-edited\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestMergeWorkingFileConflictKeepsRuntime(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// The user opened "base\n" and edited line 1; the runtime changed the same
	// line since, so the merge must conflict and keep the runtime version.
	writeRootFile(t, root, "notes.txt", "runtime\n", 0600)
	res, err := MergeWorkingFile(context.Background(), root, MergeWorkingFileRequest{
		FilePath: "notes.txt",
		Base:     []byte("base\n"),
		New:      []byte("user\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Conflict {
		t.Fatalf("expected conflict, got %+v", res)
	}
	if got := readRootFile(t, root, "notes.txt"); got != "runtime\n" {
		t.Fatalf("content = %q, want runtime kept", got)
	}
}

func TestMergeWorkingFileCleanNonOverlapping(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// base has four lines. The runtime changed line 3 and the user changed
	// line 1; the changes are non-adjacent, so a three-way merge is clean.
	writeRootFile(t, root, "notes.txt", "l1\nl2\nruntime\nl4\n", 0600)
	res, err := MergeWorkingFile(context.Background(), root, MergeWorkingFileRequest{
		FilePath: "notes.txt",
		Base:     []byte("l1\nl2\nl3\nl4\n"),
		New:      []byte("user-l1\nl2\nl3\nl4\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict {
		t.Fatalf("unexpected conflict, got %+v", res)
	}
	if got := readRootFile(t, root, "notes.txt"); got != "user-l1\nl2\nruntime\nl4\n" {
		t.Fatalf("content = %q, want merged", got)
	}
}

func TestMergeWorkingFileInvalidPath(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := MergeWorkingFile(context.Background(), root, MergeWorkingFileRequest{
		FilePath: "nope/.../escape.txt",
		New:      []byte("x"),
	}); err == nil {
		t.Fatal("expected error for invalid path")
	}
}
