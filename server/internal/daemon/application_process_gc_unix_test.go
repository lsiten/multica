//go:build !windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
)

func TestApplicationProcessParentGCRequiresStoppedOwnership(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	host := startReplacementHostFixture(t, d, command, true, true)
	retained, err := d.applicationReferencesDirectory(host.record.SourceRoot)
	if !retained || err == nil {
		t.Fatal("stopped receipt released source while actual host lock remained held")
	}
	host.release(t)
	stoppedCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err = applicationhost.WaitStopped(stoppedCtx, host.path, host.record.HostID); err != nil {
		t.Fatalf("released host ownership did not settle: %v", err)
	}
	retained, err = d.applicationReferencesDirectory(host.record.SourceRoot)
	if err != nil || retained {
		t.Fatalf("confirmed stopped ownership still unknown: retained=%t err=%v", retained, err)
	}
	redirected := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID, "redirected")
	if err = os.Symlink(t.TempDir(), redirected); err != nil {
		t.Fatal(err)
	}
	retained, err = d.applicationReferencesDirectory(host.record.SourceRoot)
	if !retained || err == nil {
		t.Fatal("redirected host inventory reported no live references")
	}
	if err = os.Remove(redirected); err != nil {
		t.Fatal(err)
	}
	instanceRedirect := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID, command.WorkspaceID, "redirected-instance")
	if err = os.Symlink(t.TempDir(), instanceRedirect); err != nil {
		t.Fatal(err)
	}
	retained, err = d.applicationReferencesDirectory(host.record.SourceRoot)
	if !retained || err == nil {
		t.Fatal("redirected instance inventory reported no live references")
	}
	t.Log("stopped receipt with live kernel lock retained source; exact exit released; both workspace and instance redirects retained conservatively")
}
