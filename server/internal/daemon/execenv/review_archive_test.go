package execenv

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveReviewReceiptsWithoutDirectoryBinding(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "ws", "task")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(root, "ws", "task"); err != nil {
		t.Fatal(err)
	}
	name := ".local-review-" + strings.Repeat("a", 64) + ".json"
	receipt := []byte(`{"snapshot_id":"snapshot","state":"approved","events":[{"kind":"approve"}]}`)
	if err := os.WriteFile(filepath.Join(root, name), receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveReviewDirectory(workspace, root); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(ReviewArchivePath(workspace, "ws", "task"), name))
	if err != nil || !bytes.Equal(archived, receipt) {
		t.Fatal("ordinary managed review history was not archived", err)
	}
}
