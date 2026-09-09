package localreview

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPatchMemoBoundsReferences(t *testing.T) {
	var memo patchMemo
	for index := 0; index <= patchMemoCapacity; index++ {
		memo.put(patchMemoKey{Root: fmt.Sprint(index)}, BlobRef{ID: "reference"})
	}
	if len(memo.entries) != patchMemoCapacity || len(memo.order) != patchMemoCapacity {
		t.Fatal("patch references exceeded capacity")
	}
	if _, exists := memo.get(patchMemoKey{Root: "0"}); exists {
		t.Fatal("oldest patch reference was not evicted")
	}
}

func TestFilePatchMemoValidatesDiskAndRegeneratesOnlyMissingContent(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprint("removed=", removed), func(t *testing.T) {
			store, err := OpenBlobStore(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			content, err := store.Put(t.Context(), strings.NewReader("fixed contents\n"))
			if err != nil {
				t.Fatal(err)
			}
			file := VersionFile{New: &content, NewMode: "100644", Preview: "text"}
			patch, err := store.FilePatch(t.Context(), file)
			if err != nil {
				t.Fatal(err)
			}
			if removed {
				err = store.root.Remove(patch.ID)
			} else {
				var output *os.File
				output, err = store.root.OpenFile(patch.ID, os.O_WRONLY|os.O_TRUNC, 0600)
				if err == nil {
					_, writeErr := output.WriteString(strings.Repeat("x", int(patch.Size)))
					err = errors.Join(writeErr, output.Close())
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			again, err := store.FilePatch(t.Context(), file)
			if removed {
				if err != nil || again != patch {
					t.Fatalf("missing patch was not regenerated: %v", err)
				}
			} else if !errors.Is(err, ErrSnapshotContentChanged) {
				t.Fatalf("tampered patch was accepted or silently replaced: %v", err)
			}
		})
	}
}
