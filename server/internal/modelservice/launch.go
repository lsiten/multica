package modelservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// Launch starts the exact installed binary with a private bootstrap and allowlisted
// environment. Recovery requires a proved no-descendant inventory; unknowns fail closed.
func Launch(ctx context.Context, root string, scope runtimeproc.Scope, executable, build string) (*Client, error) {
	if scope.Service != "ai" {
		return nil, errors.New("unsupported process service")
	}
	root, err := canonicalCacheRoot(root)
	if err != nil {
		return nil, err
	}
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		return nil, err
	}
	old, err := runtimeproc.InspectRecord(root, scope)
	needsRecovery := old.State != "stopped"
	for _, receipt := range old.Operations {
		if receipt.State != "completed" {
			needsRecovery = true
		}
	}
	if err == nil && needsRecovery {
		err = runtimeproc.Reconcile(ctx, root, old.Identity, func(ctx context.Context, record runtimeproc.Record) (runtimeproc.Reconciliation, error) {
			inv, err := readInventory(root)
			if err != nil {
				return runtimeproc.Reconciliation{}, err
			}
			if inv.InstanceID != record.Identity.InstanceID || inv.DescendantsPossible {
				return runtimeproc.Reconciliation{}, errors.New("prior model descendants are unconfirmed")
			}
			manager, err := jevmodels.New(ctx, jevmodels.Config{RootDir: root, PythonPath: "unused-during-inspection"})
			if err != nil {
				return runtimeproc.Reconciliation{}, err
			}

			for _, model := range manager.Catalog() {
				if _, err = manager.Status(model.ID, model.Revision); err != nil {
					manager.Close()
					return runtimeproc.Reconciliation{}, err
				}
			}
			raw, err := json.Marshal(inv)
			if err != nil {
				manager.Close()
				return runtimeproc.Reconciliation{}, err
			}
			return runtimeproc.Reconciliation{Inventory: raw, Release: manager.Close}, nil
		})
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	identity, err := runtimeproc.NewIdentity(scope, build)
	if err != nil {
		return nil, err
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		return nil, err
	}
	// Never copy the parent environment wholesale; model engine subprocesses apply
	// their own existing allowlist again before invoking Python.
	environment := modelEnvironment()
	process, err := runtimeproc.Start(ctx, runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: environment, Bootstrap: bootstrap, StartupTimeout: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	return newClient(process)
}
func modelEnvironment() map[string]string {
	environment := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(key) {
		case "PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "CUDA_VISIBLE_DEVICES", "SSL_CERT_FILE", "SSL_CERT_DIR", "MULTICA_JEV_PYTHON", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH":
			environment[key] = value
		}
	}
	return environment
}
func canonicalCacheRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("model cache must be absolute")
	}
	current := filepath.Clean(root)
	suffix := []string{}
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("model cache ancestor missing")
		}
		current = parent
	}
	canonical, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		canonical = filepath.Join(canonical, suffix[i])
	}
	return canonical, nil
}
