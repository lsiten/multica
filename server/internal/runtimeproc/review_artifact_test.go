package runtimeproc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatalf("chmod root: %v", err)
	}
	return root
}

func TestReviewArtifactNameIsAnchored(t *testing.T) {
	name := ReviewArtifactName("1:deadbeef")
	if len(name) != 69 || name[64:] != ".json" {
		t.Fatalf("name = %q, want 64 hex + .json", name)
	}
	if _, err := hex.DecodeString(name[:64]); err != nil {
		t.Fatalf("name is not hex: %v", err)
	}
	if ReviewArtifactName("1:deadbeef") != name {
		t.Fatal("name derivation is not deterministic")
	}
	if ReviewArtifactName("1:deadbeef") == ReviewArtifactName("1:00000000") {
		t.Fatal("distinct request ids must yield distinct names")
	}
}

func TestReviewArtifactPublishReadRoundTrip(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare namespace: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	name := ReviewArtifactName("req-1")
	body := []byte(`{"review":{"a":1}}`)
	if err := PublishReviewArtifact(namespace, name, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	sum := sha256.Sum256(body)
	got, err := ReadReviewArtifact(namespace, name, hex.EncodeToString(sum[:]), int64(len(body)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %s, want %s", got, body)
	}
	// A second publish of the same name must be refused (no overwrite).
	if err := PublishReviewArtifact(namespace, name, body); err == nil {
		t.Fatal("expected refusal to overwrite an existing artifact")
	}
}

func TestReviewArtifactRejectsHashAndLengthMismatch(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	name := ReviewArtifactName("req-2")
	body := []byte(`{"ok":true}`)
	if err := PublishReviewArtifact(namespace, name, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := ReadReviewArtifact(namespace, name, strings.Repeat("0", 64), int64(len(body))); err == nil {
		t.Fatal("expected hash mismatch")
	}
	if _, err := ReadReviewArtifact(namespace, name, "bad", int64(len(body))+1); err == nil {
		t.Fatal("expected length mismatch")
	}
	if _, err := ReadReviewArtifact(namespace, name, "bad", -1); err == nil {
		t.Fatal("expected rejection of negative length")
	}
}

func TestReviewArtifactRejectsNonPrivateFile(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	name := ReviewArtifactName("req-3")
	body := []byte(`{"ok":true}`)
	if err := PublishReviewArtifact(namespace, name, body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	sum := sha256.Sum256(body)
	if err := os.Chmod(filepath.Join(namespace, name), 0600|004); err != nil {
		t.Skip("chmod not supported on this filesystem")
	}
	if _, err := ReadReviewArtifact(namespace, name, hex.EncodeToString(sum[:]), int64(len(body))); err == nil {
		t.Fatal("expected private-file validation to reject a world-readable artifact")
	}
}

func TestReviewArtifactRejectsOverCap(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	name := ReviewArtifactName("req-4")
	body := make([]byte, reviewArtifactMaxOutput+1)
	body[0] = '{'
	body[len(body)-1] = '}'
	if err := PublishReviewArtifact(namespace, name, body); err == nil {
		t.Fatal("expected rejection of over-cap artifact")
	}
}

func TestReviewArtifactRejectsInvalidJSON(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	name := ReviewArtifactName("req-5")
	if err := PublishReviewArtifact(namespace, name, []byte("not-json")); err == nil {
		t.Fatal("expected rejection of invalid json artifact")
	}
}

func TestReviewArtifactPruneIsBoundedAndOrphanSafe(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	body := []byte(`{"x":1}`)
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := PublishReviewArtifact(namespace, ReviewArtifactName(id), body); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}
	keep := map[string]bool{ReviewArtifactName("d"): true}
	removed, err := PruneReviewArtifacts(namespace, keep, 2, 0)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed == 0 {
		t.Fatal("expected at least one removal to reach the count bound")
	}
	entries, _ := os.ReadDir(namespace)
	if len(entries) != 2 {
		t.Fatalf("count = %d, want 2 after prune", len(entries))
	}
	kept := map[string]bool{}
	for _, e := range entries {
		kept[e.Name()] = true
	}
	if !kept[ReviewArtifactName("d")] {
		t.Fatalf("retained artifact was pruned")
	}
	if !kept[ReviewArtifactName("a")] && !kept[ReviewArtifactName("b")] && !kept[ReviewArtifactName("c")] {
		t.Fatal("prune removed every candidate, even though the count bound allowed two")
	}
}

func TestReviewArtifactRejectsPathTraversalName(t *testing.T) {
	root := newTestRoot(t)
	if err := PrepareReviewArtifactNamespace(root); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	namespace := ReviewArtifactNamespace(root)
	if err := PublishReviewArtifact(namespace, "../escape.json", []byte(`{}`)); err == nil {
		t.Fatal("expected rejection of a path-traversal name")
	}
	if _, err := ReadReviewArtifact(namespace, "../escape.json", "bad", 1); err == nil {
		t.Fatal("expected rejection of a path-traversal name on read")
	}
}
