package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/communications"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestCommunicationReceiptsSurviveSameTaskRedispatch(t *testing.T) {
	for _, channel := range []string{"email", "phone"} {
		for _, completed := range []bool{false, true} {
			t.Run(channel+map[bool]string{false: "/pending", true: "/completed"}[completed], func(t *testing.T) {
				// Given a real execution claim and a persisted external-action receipt.
				root := t.TempDir()
				task := Task{ID: "receipt-task", WorkspaceID: "receipt-workspace"}
				params := taskRootDirParams(root, task)
				claim, err := execenv.ClaimEnvRoot(params)
				if err != nil {
					t.Fatal(err)
				}
				envRoot, err := execenv.ResolveRootDir(params)
				if err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(envRoot, "ephemeral")
				if err := os.WriteFile(marker, []byte("reset me"), 0600); err != nil {
					t.Fatal(err)
				}
				path := communicationReceiptPath(root, task, channel)
				store, err := communications.NewFileIdempotencyStore(path)
				if err != nil {
					t.Fatal(err)
				}
				if reserved, err := store.Reserve("operation"); !reserved || err != nil {
					t.Fatalf("reserve=%v err=%v", reserved, err)
				}
				if completed {
					if err := store.Complete("operation", communications.Call{SID: "accepted"}); err != nil {
						t.Fatal(err)
					}
				}
				claim.Release()

				// When the same task is re-dispatched through the production reset.
				claim, err = execenv.ClaimEnvRoot(params)
				if err != nil {
					t.Fatal(err)
				}
				defer claim.Release()
				store, err = communications.NewFileIdempotencyStore(path)
				if err != nil {
					t.Fatal(err)
				}
				reserved, err := store.Reserve("operation")

				// Then the execution root is clean, but sending again is impossible.
				if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("execution root was not reset: %v", statErr)
				}
				if reserved {
					t.Fatal("external operation could be repeated")
				}
				if completed && err != nil {
					t.Fatal(err)
				}
				if !completed && !errors.Is(err, communications.ErrAmbiguousOperation) {
					t.Fatalf("pending receipt must fail closed: %v", err)
				}
			})
		}
	}
}
