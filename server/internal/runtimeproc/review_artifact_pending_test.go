package runtimeproc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPendingReviewArtifactSurvivesFinalArtifactFailureAndIsPrivate(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(root, 0700)
	request := "worker:execution"
	body, _ := json.Marshal(map[string]string{"result": strings.Repeat("x", 32)})
	if err := PersistReviewArtifactPending(root, request, body); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReviewArtifactPending(root, request)
	if err != nil || string(got) != string(body) {
		t.Fatalf("pending result lost: %v", err)
	}
	entries, err := ListReviewArtifactPending(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("pending list=%d err=%v", len(entries), err)
	}
	if err := RemoveReviewArtifactPending(root, request); err != nil {
		t.Fatal(err)
	}
	if entries, err := ListReviewArtifactPending(root); err != nil || len(entries) != 0 {
		t.Fatalf("pending result not removed: %d %v", len(entries), err)
	}
}
