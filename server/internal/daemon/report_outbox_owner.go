package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrReportOutboxOwned means another live process holds the single-owner lock
// for this outbox namespace. Only the owner may upload a namespace's terminal
// and JEV reports; a second process must defer to it instead of delivering the
// same report twice. It is the runtime expression of the F2 "sole report
// uploader" invariant: control stops being a second uploader once a gateway
// (or any other process) owns the namespace.
var ErrReportOutboxOwned = errors.New("report outbox is owned by another process")

// reportOutboxOwner is the kernel-protected single-owner handle for one outbox
// namespace. The lock is an OS flock / LOCKFILE_EXCLUSIVE on a private file, so
// it is released automatically when the owning process exits — a crashed
// uploader can never strand a namespace forever. Closing the file releases it
// while the process lives, which is how a deferred loop gives up ownership so a
// successor can take over.
type reportOutboxOwner struct {
	file *os.File
}

// release hands the single-owner lock back so another process (for example a
// gateway taking over after a control restart) can become the uploader. A
// double release is a no-op.
func (o *reportOutboxOwner) release() {
	if o == nil || o.file == nil {
		return
	}
	_ = o.file.Close()
	o.file = nil
}

// reportOutboxNamespaceRoot is the shared, single-owner namespace root used by
// both the terminal-report store and the JEV decision store. Both stores derive
// their per-daemon hash from the same backend/profile/daemon identity, so one
// owner lock at this shared root guards both queues behind a single owner.
func (d *Daemon) reportOutboxNamespaceRoot() string {
	return filepath.Join(d.cfg.WorkspacesRoot, ".report-outbox")
}

// reportOutboxOwnerLockFile returns the private, single-owner lock file for the
// daemon's current namespace. It is namespaced by backend, authenticated
// account, profile and daemon so that a control and a gateway on the same
// account/profile/daemon land on one shared owner lock (the sole-uploader
// guard is real) while a different account gets a disjoint lock and never
// adopts the other's queue. The account is resolved once at startup; when it
// is unknown the lock still names backend/profile/daemon, which stays safe
// but cannot cross a gateway boundary until discovery succeeds. It is stable
// across re-instantiations of the same identity, so a restarted process lands
// on the lock a live owner already holds instead of double-delivering.
func (d *Daemon) reportOutboxOwnerLockFile() string {
	identity := strings.TrimRight(d.cfg.ServerBaseURL, "/") + "\x00" + d.accountID + "\x00" + d.cfg.Profile + "\x00" + d.cfg.DaemonID
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(d.reportOutboxNamespaceRoot(), hex.EncodeToString(sum[:16]), "owner.lock")
}

// resolveReportOutboxAccount fetches the authenticated account once, before the
// single-owner lock is acquired, so the lock can be namespaced by account. It
// is best-effort: a transient or unavailable /api/me leaves accountID empty and
// the lock falls back to a per-daemon namespace rather than failing startup,
// because the outbox is a durability backstop and must not block the daemon.
func (d *Daemon) resolveReportOutboxAccount(ctx context.Context) {
	if d.client == nil {
		return
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(ctx, "/api/me", &account); err != nil {
		d.logger.Warn("resolve report outbox account", "error", err)
		return
	}
	if account.ID != "" {
		d.accountID = account.ID
	}
}

// acquireReportOutboxOwner takes the non-blocking single-owner lock for the
// daemon's outbox namespace and records it on the daemon. A second owner
// returns ErrReportOutboxOwned so the caller can defer; a transient failure
// (disk, permissions) is returned unwrapped so the loop keeps its normal backoff
// and retry instead of treating a flaky disk as a permanent second owner.
func (d *Daemon) acquireReportOutboxOwner() error {
	if strings.TrimSpace(d.cfg.WorkspacesRoot) == "" {
		// No outbox is configured, so there is nothing to upload and no owner to
		// own. The loops treat a nil owner exactly as "not the uploader".
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.reportOutboxOwnerLockFile()), 0o700); err != nil {
		return fmt.Errorf("create report outbox owner dir: %w", err)
	}
	file, owned, err := lockReportOutboxDomain(d.reportOutboxOwnerLockFile())
	if err != nil {
		return err
	}
	if owned {
		return ErrReportOutboxOwned
	}
	d.reportOutboxOwner = &reportOutboxOwner{file: file}
	return nil
}
