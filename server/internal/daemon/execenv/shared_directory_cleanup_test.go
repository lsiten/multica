package execenv

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedDirectoryCleanupProtectsDescendantBorrowersAndPendingDeliveries(t *testing.T) {
	for _, protection := range []string{"live", "delivery"} {
		t.Run(protection, func(t *testing.T) {
			ctx := context.Background()
			root := filepath.Join(t.TempDir(), "owned")
			child := filepath.Join(root, "nested-source")
			if err := os.MkdirAll(child, 0700); err != nil {
				t.Fatal(err)
			}
			var lease *SharedDirectoryLease
			var delivery string
			if protection == "live" {
				var err error
				lease, err = UseSharedDirectory(ctx, child)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { lease.Finish(ctx, nil) })
			} else {
				canonical, err := filepath.EvalSymlinks(child)
				if err != nil {
					t.Fatal(err)
				}
				stateDir, err := sharedDirectoryStateDir(canonical)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(stateDir, 0700); err != nil {
					t.Fatal(err)
				}
				delivery = filepath.Join(stateDir, "delivery-fixture.json")
				if err := os.WriteFile(delivery, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Remove(delivery) })
			}
			canonicalRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			pruned, err := PruneUnusedSharedDirectory(ctx, canonicalRoot)
			if err != nil || pruned {
				t.Fatalf("protected descendant was deleted: pruned=%v error=%v", pruned, err)
			}
			if lease != nil {
				if err := lease.Finish(ctx, nil); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(delivery); err != nil {
				t.Fatal(err)
			}
			pruned, err = PruneUnusedSharedDirectory(ctx, canonicalRoot)
			if err != nil || !pruned {
				t.Fatalf("unreferenced private tree was not collected: pruned=%v error=%v", pruned, err)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("private tree remains after collection: %v", err)
			}
			if _, err := UseSharedDirectory(ctx, child); err == nil {
				t.Fatal("borrower entered a directory after cleanup")
			}
		})
	}
}
