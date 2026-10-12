package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type localAuthorityWorkspace struct {
	ID       string    `json:"id"`
	Runtimes []Runtime `json:"runtimes"`
}

func retryableBootstrapError(err error) bool {
	var response *requestError
	if errors.As(err, &response) {
		return response.StatusCode >= 500 || response.StatusCode == 429
	}
	var transport net.Error
	return errors.As(err, &transport) || errors.Is(err, context.DeadlineExceeded)
}

func (d *Daemon) awaitRemoteBootstrap(ctx context.Context) error {
	// No identity means no task claims, report uploads, or child owner binding.
	// The authenticated local control server remains available during recovery.
	d.remoteBootstrapPending.Store(true)
	d.ready.Store(true)
	d.logger.Warn("local daemon available; remote bootstrap deferred")
	timer := time.NewTimer(0)
	defer timer.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		err := d.preflightAuth(ctx)
		if err == nil {
			_, err = d.verifiedAccount(ctx)
			if err == nil {
				if err := d.saveLocalAuthority(); err != nil {
					d.logger.Debug("persist recovered local authority", "error", err)
				}
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryableBootstrapError(err) {
			return err
		}
		failures++
		timer.Reset(reportRetryDelay(failures, err))
	}
}

type localAuthoritySnapshot struct {
	Version    int                       `json:"version"`
	Backend    string                    `json:"backend"`
	Profile    string                    `json:"profile"`
	DaemonID   string                    `json:"daemon_id"`
	TokenHash  string                    `json:"token_hash"`
	AccountID  string                    `json:"account_id"`
	Workspaces []localAuthorityWorkspace `json:"workspaces"`
}

func (d *Daemon) localAuthorityPath() string {
	key := sha256.Sum256([]byte(strings.TrimRight(d.cfg.ServerBaseURL, "/") + "\x00" + d.cfg.Profile + "\x00" + d.cfg.DaemonID))
	root := d.cfg.WorkspacesRoot
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	return filepath.Join(root, ".local-authority", hex.EncodeToString(key[:16]), "identity.json")
}

func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) saveLocalAuthority() error {
	d.mu.Lock()
	snapshot := localAuthoritySnapshot{Version: 1, Backend: strings.TrimRight(d.cfg.ServerBaseURL, "/"), Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, AccountID: d.accountID, TokenHash: tokenFingerprint(d.client.Token())}
	for id, workspace := range d.workspaces {
		entry := localAuthorityWorkspace{ID: id}
		for _, rid := range workspace.runtimeIDs {
			if runtime, ok := d.runtimeIndex[rid]; ok {
				entry.Runtimes = append(entry.Runtimes, runtime)
			}
		}
		snapshot.Workspaces = append(snapshot.Workspaces, entry)
	}
	d.mu.Unlock()
	if snapshot.AccountID == "" {
		return errors.New("verified local account identity unavailable")
	}
	path := d.localAuthorityPath()
	if err := runtimeproc.PrepareRoot(filepath.Dir(path)); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := runtimeproc.ProtectPrivateArtifact(file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := runtimeproc.ReplacePrivateArtifact(file.Name(), path); err != nil {
		return err
	}
	return runtimeproc.SyncPrivateArtifactDirectory(filepath.Dir(path))
}

func (d *Daemon) loadLocalAuthority() bool {
	if d.client == nil || d.client.Token() == "" || d.cfg.WorkspacesRoot == "" {
		return false
	}
	path := d.localAuthorityPath()
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || runtimeproc.ValidatePrivateArtifact(file) != nil {
		return false
	}
	var snapshot localAuthoritySnapshot
	if json.NewDecoder(file).Decode(&snapshot) != nil || snapshot.Version != 1 || snapshot.AccountID == "" || snapshot.TokenHash != tokenFingerprint(d.client.Token()) || snapshot.Backend != strings.TrimRight(d.cfg.ServerBaseURL, "/") || snapshot.Profile != d.cfg.Profile || snapshot.DaemonID != d.cfg.DaemonID {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accountID = snapshot.AccountID
	for _, entry := range snapshot.Workspaces {
		workspace := &workspaceState{workspaceID: entry.ID}
		for _, runtime := range entry.Runtimes {
			workspace.runtimeIDs = append(workspace.runtimeIDs, runtime.ID)
			d.runtimeIndex[runtime.ID] = runtime
		}
		d.workspaces[entry.ID] = workspace
	}
	return true
}

func (d *Daemon) verifiedAccount(ctx context.Context) (string, error) {
	d.mu.Lock()
	account := d.accountID
	d.mu.Unlock()
	if account != "" {
		return account, nil
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(ctx, "/api/me", &identity); err != nil {
		return "", err
	}
	if identity.ID == "" {
		return "", errors.New("verified account identity missing")
	}
	d.mu.Lock()
	d.accountID = identity.ID
	d.mu.Unlock()
	return identity.ID, nil
}
