package daemon

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/util"
)

type executionEnvClaim struct {
	claim   *execenv.EnvRootClaim
	shared  bool
	release func()
	once    sync.Once
}

func (c *executionEnvClaim) Release() {
	if c != nil {
		c.once.Do(c.release)
	}
}

func (c *executionEnvClaim) RootDir() string {
	if c == nil {
		return ""
	}
	return c.claim.RootDir()
}

type executionEnvClaimEntry struct {
	claim *execenv.EnvRootClaim
	info  os.FileInfo
	users int
}

func executionEnvClaimKey(root string) string {
	if canonical, err := util.ResolveSymlinks(root); err == nil {
		return canonical
	}
	return filepath.Clean(root)
}

func (d *Daemon) registerExecutionEnvClaim(claim *execenv.EnvRootClaim, info os.FileInfo) *executionEnvClaim {
	if claim == nil {
		return nil
	}
	root := executionEnvClaimKey(claim.RootDir())
	if info == nil {
		var err error
		info, err = os.Stat(root)
		if err != nil {
			return &executionEnvClaim{claim: claim, release: claim.Release}
		}
	}
	d.executionEnvClaimsMu.Lock()
	defer d.executionEnvClaimsMu.Unlock()
	if d.executionEnvClaims == nil {
		d.executionEnvClaims = make(map[string]*executionEnvClaimEntry)
	}
	entry := &executionEnvClaimEntry{claim: claim, info: info, users: 1}
	d.executionEnvClaims[root] = entry
	return d.executionEnvClaimHandle(root, entry, false)
}

func (d *Daemon) borrowExecutionEnvClaim(root string) (*executionEnvClaim, os.FileInfo) {
	root = executionEnvClaimKey(root)
	d.executionEnvClaimsMu.Lock()
	defer d.executionEnvClaimsMu.Unlock()
	entry := d.executionEnvClaims[root]
	if entry == nil {
		return nil, nil
	}
	entry.users++
	return d.executionEnvClaimHandle(root, entry, true), entry.info
}

func (d *Daemon) executionEnvClaimHandle(root string, entry *executionEnvClaimEntry, shared bool) *executionEnvClaim {
	return &executionEnvClaim{claim: entry.claim, shared: shared, release: func() {
		d.executionEnvClaimsMu.Lock()
		defer d.executionEnvClaimsMu.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(d.executionEnvClaims, root)
			// Keep the kernel lock until every concurrent user has finished.
			entry.claim.Release()
		}
	}}
}
