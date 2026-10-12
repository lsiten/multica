package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func (d *Daemon) lockLocalApplicationIntent(ctx context.Context, instanceID string) (func(), error) {
	lock := d.applicationInstanceLock("local-command/" + instanceID)
	if err := lockApplicationUntil(ctx, lock); err != nil {
		return nil, err
	}
	path := d.localIntentPath(instanceID) + ".lock"
	if err := runtimeproc.PrepareRoot(filepath.Dir(path)); err != nil {
		lock.Unlock()
		return nil, err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		file, owned, err := lockReportOutboxDomain(path)
		if err != nil {
			lock.Unlock()
			return nil, err
		}
		if !owned {
			return func() { file.Close(); lock.Unlock() }, nil
		}
		select {
		case <-ctx.Done():
			lock.Unlock()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

type localApplicationIntent struct {
	Input          localApplicationRequest `json:"input"`
	InstanceID     string                  `json:"instance_id"`
	Generation     int64                   `json:"generation"`
	PreviousHostID string                  `json:"previous_host_id"`
	DesiredState   string                  `json:"desired_state"`
	Synced         bool                    `json:"synced"`
	Result         *localApplicationResult `json:"result,omitempty"`
}

func (d *Daemon) localIntentPath(instanceID string) string {
	key := sha256.Sum256([]byte(instanceID + "\x00" + d.accountID))
	return filepath.Join(filepath.Dir(d.localAuthorityPath()), "application-intents", hex.EncodeToString(key[:16])+".json")
}

func (d *Daemon) readLocalIntent(instanceID string) (*localApplicationIntent, error) {
	intent, err := readApplicationIntent(d.localIntentPath(instanceID), instanceID)
	if err == nil && intent != nil && intent.Result == nil {
		receipt, receiptErr := d.readLocalCommand(instanceID, intent.Input.CommandID)
		if receiptErr != nil {
			return nil, receiptErr
		}
		if receipt != nil && receipt.Result != nil {
			return receipt, nil
		}
	}
	return intent, err
}

func (d *Daemon) localCommandPath(instanceID, commandID string) string {
	key := sha256.Sum256([]byte(d.accountID + "\x00" + instanceID + "\x00" + commandID))
	return filepath.Join(filepath.Dir(d.localIntentPath(instanceID)), "receipts", hex.EncodeToString(key[:])+".json")
}

func (d *Daemon) readLocalCommand(instanceID, commandID string) (*localApplicationIntent, error) {
	intent, err := readApplicationIntent(d.localCommandPath(instanceID, commandID), instanceID)
	if err == nil && intent != nil && intent.Input.CommandID != commandID {
		return nil, errors.New("local command receipt identity mismatch")
	}
	return intent, err
}

func readApplicationIntent(path, instanceID string) (*localApplicationIntent, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("local application intent storage is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || runtimeproc.ValidatePrivateArtifact(file) != nil {
		return nil, errors.New("local application intent ownership changed")
	}
	var intent localApplicationIntent
	if err := json.NewDecoder(file).Decode(&intent); err != nil {
		return nil, err
	}
	if intent.InstanceID != instanceID {
		return nil, errors.New("local application intent identity mismatch")
	}
	return &intent, nil
}

func (d *Daemon) writeLocalIntent(intent localApplicationIntent) error {
	if err := writeApplicationIntent(d.localCommandPath(intent.InstanceID, intent.Input.CommandID), intent); err != nil {
		return err
	}
	return writeApplicationIntent(d.localIntentPath(intent.InstanceID), intent)
}

func writeApplicationIntent(path string, intent localApplicationIntent) error {
	if err := runtimeproc.PrepareRoot(filepath.Dir(path)); err != nil {
		return err
	}
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".intent-*")
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
