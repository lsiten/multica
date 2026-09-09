package localreview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBranchesRejectsEnclosingRepository(t *testing.T) {
	repo := repository(t)
	child := filepath.Join(repo, "context-only")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Branches(t.Context(), child); err == nil {
		t.Fatal("exposed enclosing repository branches")
	}
}
