package workspacefiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTrashPagesSurviveReopeningAndExcludeRestoredEntries(t *testing.T) {
	root, path := fixture(t)
	for _, name := range []string{"first.md", "second.md", "third.md"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(context.Background(), root, request(name, protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: name})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Apply(context.Background(), root, request("restore", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "first.md", RecoveryID: "first.md"})); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var found []string
	offset := 0
	for {
		page, err := ListTrash(context.Background(), Scope{Working: reopened, Storage: root.Storage}, TrashPageRequest{Offset: offset, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			found = append(found, entry.Path)
		}
		if !page.HasMore {
			break
		}
		if page.NextOffset <= offset || page.NextOffset-offset > 2 {
			t.Fatalf("invalid bounded cursor: %+v", page)
		}
		offset = page.NextOffset
	}
	slices.Sort(found)
	if !slices.Equal(found, []string{"second.md", "third.md"}) {
		t.Fatalf("recoverable entries: %v", found)
	}
}

func TestReadingTrashDoesNotCreateMetadata(t *testing.T) {
	root, path := fixture(t)
	page, err := ListTrash(context.Background(), root, TrashPageRequest{Limit: 100})
	if err != nil || len(page.Entries) != 0 || page.HasMore {
		t.Fatalf("empty trash: %+v, error: %v", page, err)
	}
	if _, err := os.Stat(filepath.Join(path, ".multica")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created metadata: %v", err)
	}
}
