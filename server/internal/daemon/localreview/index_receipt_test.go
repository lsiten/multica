package localreview

import (
	"strings"
	"testing"
)

func TestIndexReceiptRejectsOperationIDReuseWithDifferentActor(t *testing.T) {
	root := t.TempDir()
	request := IndexOperationRequest{Path: "/repo", CommandID: "stage-1", ActorID: "owner", Action: "stage", VersionID: strings.Repeat("a", 64), IndexID: strings.Repeat("b", 64), Paths: []string{"app.txt"}}
	prepared := IndexOperationResult{IndexID: strings.Repeat("c", 64)}
	if err := PrepareIndexReceipt(root, request, prepared); err != nil {
		t.Fatal(err)
	}
	receipt, exists, err := LoadIndexReceipt(root, request)
	if err != nil || !exists || receipt.State != "prepared" {
		t.Fatal("missing prepared receipt", err)
	}
	request.ActorID = "other"
	if _, _, err := LoadIndexReceipt(root, request); err == nil {
		t.Fatal("another actor reused operation receipt")
	}
}

func TestIndexReceiptCompletesAndRetainsVersionReference(t *testing.T) {
	root := t.TempDir()
	request := IndexOperationRequest{Path: "/repo", CommandID: "unstage-1", ActorID: "owner", Action: "unstage", VersionID: strings.Repeat("a", 64), IndexID: strings.Repeat("b", 64), Paths: []string{"b.txt", "a.txt"}}
	result := IndexOperationResult{IndexID: strings.Repeat("c", 64)}
	if err := PrepareIndexReceipt(root, request, result); err != nil {
		t.Fatal(err)
	}
	request.Paths = []string{"a.txt", "b.txt"}
	if err := CompleteIndexReceipt(root, request); err != nil {
		t.Fatal(err)
	}
	receipt, exists, err := LoadIndexReceipt(root, request)
	if err != nil || !exists || receipt.State != "completed" || receipt.Result.IndexID != result.IndexID {
		t.Fatal("completion replay lost result", err)
	}
	ids, err := ReferencedVersions(t.Context(), root)
	if err != nil || len(ids) != 1 || ids[0] != request.VersionID {
		t.Fatal("receipt snapshot not protected", err)
	}
}
