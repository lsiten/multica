package daemon

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func reportOutboxTestDaemon(root, profile, daemonID string) *Daemon {
	return &Daemon{
		cfg: Config{
			ServerBaseURL:  "https://api.example.test",
			Profile:        profile,
			DaemonID:       daemonID,
			WorkspacesRoot: root,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestReportOutboxOwnerSecondOwnerIsDenied is the core single-owner guarantee:
// the same namespace can only be uploaded by one live process at a time. A
// second owner on the same backend/profile/daemon identity must be refused so
// control never double-delivers a terminal or JEV report the gateway owns.
func TestReportOutboxOwnerSecondOwnerIsDenied(t *testing.T) {
	root := t.TempDir()
	first := reportOutboxTestDaemon(root, "work", "daemon-1")
	second := reportOutboxTestDaemon(root, "work", "daemon-1")

	if err := first.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("first acquire report outbox owner: %v", err)
	}
	if first.reportOutboxOwner == nil {
		t.Fatal("first owner was not recorded")
	}
	if first.reportOutboxOwnerLockFile() != second.reportOutboxOwnerLockFile() {
		t.Fatal("two daemons with the same identity computed different lock files")
	}

	err := second.acquireReportOutboxOwner()
	if !errors.Is(err, ErrReportOutboxOwned) {
		t.Fatalf("second acquire: got %v, want ErrReportOutboxOwned", err)
	}
	if second.reportOutboxOwner != nil {
		t.Fatal("denied second owner must not record an owner handle")
	}
}

// TestReportOutboxOwnerReleaseLetsSuccessorTakeOver proves the lock is not
// stranded: once the owner releases, another process can acquire the same
// namespace. This is how a gateway becomes the sole uploader after a control
// restart.
func TestReportOutboxOwnerReleaseLetsSuccessorTakeOver(t *testing.T) {
	root := t.TempDir()
	first := reportOutboxTestDaemon(root, "work", "daemon-1")
	second := reportOutboxTestDaemon(root, "work", "daemon-1")

	if err := first.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.reportOutboxOwner.release()
	if first.reportOutboxOwner.file != nil {
		t.Fatal("release did not close the owner handle")
	}

	if err := second.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("successor acquire after release: %v", err)
	}
	if second.reportOutboxOwner == nil {
		t.Fatal("successor did not acquire the owner handle")
	}
	second.reportOutboxOwner.release()
}

// TestReportOutboxOwnerNoOutboxIsNotBlocked: with no workspaces root there is no
// outbox and therefore no owner to own. Acquisition succeeds with a nil owner
// and the daemon is not marked blocked, so the loops keep their existing
// no-op-on-nil behavior.
func TestReportOutboxOwnerNoOutboxIsNotBlocked(t *testing.T) {
	d := reportOutboxTestDaemon("", "work", "daemon-1")
	if err := d.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("acquire with no outbox: %v", err)
	}
	if d.reportOutboxOwner != nil {
		t.Fatal("no outbox must not record an owner handle")
	}
	if d.reportOutboxBlocked {
		t.Fatal("no outbox must not be marked blocked")
	}
}

// TestReportOutboxOwnerLockFileIsStable locks the single-owner boundary to a
// fixed location derived from backend/profile/daemon, so a re-instantiated
// daemon (control after a restart) lands on the same lock the gateway already
// holds instead of silently becoming a second uploader.
func TestReportOutboxOwnerLockFileIsStable(t *testing.T) {
	root := t.TempDir()
	a := reportOutboxTestDaemon(root, "work", "daemon-1")
	b := reportOutboxTestDaemon(root, "work", "daemon-1")
	if a.reportOutboxOwnerLockFile() != b.reportOutboxOwnerLockFile() {
		t.Fatalf("lock file must be stable: %q vs %q", a.reportOutboxOwnerLockFile(), b.reportOutboxOwnerLockFile())
	}
	want := filepath.Join(root, ".report-outbox")
	if !filepath.HasPrefix(a.reportOutboxOwnerLockFile(), want) {
		t.Fatalf("lock file %q must live under %q", a.reportOutboxOwnerLockFile(), want)
	}
}

// TestReportOutboxOwnerSameAccountSharesOneOwnerLock is the effective-guard case
// the F2 split needs: control and a gateway on the same account/profile/daemon
// land on one shared owner lock, so the second is denied and only one uploads.
// This is what makes the sole-uploader guard real instead of a per-daemon
// no-op.
func TestReportOutboxOwnerSameAccountSharesOneOwnerLock(t *testing.T) {
	root := t.TempDir()
	control := reportOutboxTestDaemon(root, "work", "daemon-1")
	gateway := reportOutboxTestDaemon(root, "work", "daemon-1")
	// Same account resolves to the same lock file even though they are separate
	// processes; that is the shared-namespace boundary.
	control.accountID = "acct-1"
	gateway.accountID = "acct-1"
	if control.reportOutboxOwnerLockFile() != gateway.reportOutboxOwnerLockFile() {
		t.Fatalf("same account must share one owner lock: %q vs %q", control.reportOutboxOwnerLockFile(), gateway.reportOutboxOwnerLockFile())
	}
	if err := control.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("control acquire: %v", err)
	}
	if err := gateway.acquireReportOutboxOwner(); !errors.Is(err, ErrReportOutboxOwned) {
		t.Fatalf("gateway acquire: got %v, want ErrReportOutboxOwned", err)
	}
}

// TestReportOutboxOwnerDifferentAccountIsDisjoint proves a different account
// never adopts another's owner lock or queue: the namespaces are disjoint, so
// each account keeps its own uploader and a login change cannot silently replay
// the other account's pending reports.
func TestReportOutboxOwnerDifferentAccountIsDisjoint(t *testing.T) {
	root := t.TempDir()
	a := reportOutboxTestDaemon(root, "work", "daemon-1")
	b := reportOutboxTestDaemon(root, "work", "daemon-1")
	a.accountID = "acct-a"
	b.accountID = "acct-b"
	if a.reportOutboxOwnerLockFile() == b.reportOutboxOwnerLockFile() {
		t.Fatal("different accounts must get disjoint owner locks")
	}
	if err := a.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("a acquire: %v", err)
	}
	if err := b.acquireReportOutboxOwner(); err != nil {
		t.Fatalf("b on a disjoint namespace must still acquire: %v", err)
	}
}
