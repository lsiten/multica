package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestSharedExecutionEnvClaimProtectsRootUntilLastUserFinishes(t *testing.T) {
	base := t.TempDir()
	raw, err := execenv.ClaimEnvRoot(execenv.RootDirParams{WorkspacesRoot: base, WorkspaceID: "workspace", TaskID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	info, err := os.Stat(raw.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := d.registerExecutionEnvClaim(raw, info)
	defer owner.Release()
	first, borrowedInfo := d.borrowExecutionEnvClaim(raw.RootDir())
	second, _ := d.borrowExecutionEnvClaim(raw.RootDir())
	if first == nil || second == nil || !first.shared || !second.shared {
		t.Fatal("concurrent users could not borrow the execution claim")
	}
	if !os.SameFile(info, borrowedInfo) {
		t.Fatal("shared claim lost the pinned directory identity")
	}
	defer first.Release()
	defer second.Release()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rel, err := filepath.Rel(base, raw.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	assertBusy := func() {
		t.Helper()
		claim, _, err := execenv.LockEnvRootForReuse(root, rel, raw.RootDir())
		if claim != nil {
			claim.Release()
		}
		if !errors.Is(err, execenv.ErrEnvRootBusy) {
			t.Fatalf("shared checkout became reclaimable: %v", err)
		}
	}
	owner.Release()
	owner.Release()
	assertBusy()
	first.Release()
	assertBusy()
	second.Release()
	claim, _, err := execenv.LockEnvRootForReuse(root, rel, raw.RootDir())
	if err != nil || claim == nil {
		t.Fatalf("last shared user did not release the kernel lock: %v", err)
	}
	claim.Release()
	if len(d.executionEnvClaims) != 0 {
		t.Fatal("execution claim references leaked")
	}
}
