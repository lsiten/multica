package localreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRecordRejectsSymlinkReceipt(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"state":"approved"}`), 0600); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	if err := os.Symlink(outside, filepath.Join(root, ".local-review-"+key+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecord(root, key); err == nil {
		t.Fatal("followed a replaced receipt outside the task root")
	}
}
