package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeStorageClassifiesResourcesAndDeduplicatesHardlinks(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"worktree/source.go", "output/result.txt", "logbook/run.log", "codex-home/session", "metadata"} {
		writeLifecycleFile(t, filepath.Join(root, path), "contents")
	}
	out := t.TempDir()
	writeLifecycleFile(t, filepath.Join(out, "outside"), "outside")
	if err := os.Symlink(out, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	first, err := scanWorktreeStorage(t.Context(), root, seen)
	if err != nil {
		t.Fatal(err)
	}
	if first.CodeBytes != 8 || first.OutputBytes != 8 || first.LogBytes != 8 || first.RuntimeBytes != 8 || first.OtherBytes != 8 {
		t.Fatalf("invalid classification: %+v", first)
	}
	linked := t.TempDir()
	if err := os.Link(filepath.Join(root, "metadata"), filepath.Join(linked, "shared")); err != nil {
		t.Fatal(err)
	}
	second, err := scanWorktreeStorage(t.Context(), linked, seen)
	if err != nil {
		t.Fatal(err)
	}
	if second.OtherBytes != 8 {
		t.Fatalf("logical bytes lost: %+v", second)
	}
	if first.AllocatedBytes != nil && (second.AllocatedBytes == nil || *second.AllocatedBytes != 0) {
		t.Fatalf("shared inode counted twice: %+v", second)
	}
}
