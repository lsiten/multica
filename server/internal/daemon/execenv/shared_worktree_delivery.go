package execenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// SharedWorktreeDelivery is a receipt awaiting the shared branch's final commit.
// Namespace binds replay to the daemon's existing authenticated report outbox.
type SharedWorktreeDelivery struct {
	TaskID    string `json:"task_id"`
	Namespace string `json:"namespace"`
	WorkDir   string `json:"work_dir"`
	Branch    string `json:"branch"`
	Commit    string `json:"commit,omitempty"`
	NoWork    bool   `json:"no_work,omitempty"`
	// FilePath is the on-disk receipt location. It is carried over the
	// environment service boundary (omitempty, so bind-time marshaling is
	// unchanged) and is always re-derived from the real path by readSharedDelivery.
	FilePath string `json:"file_path,omitempty"`
}

func sharedDeliveryPrefix(namespace string) string {
	hash := sha256.Sum256([]byte(namespace))
	return "delivery-" + hex.EncodeToString(hash[:]) + "-"
}

func sharedDeliveryFile(dir, task, namespace string) string {
	hash := sha256.Sum256([]byte(task))
	return filepath.Join(dir, sharedDeliveryPrefix(namespace)+hex.EncodeToString(hash[:])+".json")
}

func preserveSharedWorktreeReceipt(dir string, receipt SharedWorktreeDelivery) error {
	if receipt.TaskID == "" || receipt.Namespace == "" {
		return nil
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return writeFileAtomic(sharedDeliveryFile(dir, receipt.TaskID, receipt.Namespace), data, 0600)
}

func (l *SharedDirectoryLease) bindWorktreeRun(receipt SharedWorktreeDelivery) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err := l.file.WriteAt(data, 0); err != nil {
		return err
	}
	if err := l.file.Sync(); err != nil {
		return err
	}
	l.run = receipt
	return nil
}

func readSharedDelivery(path string) (SharedWorktreeDelivery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SharedWorktreeDelivery{}, err
	}
	var receipt SharedWorktreeDelivery
	err = json.Unmarshal(data, &receipt)
	if err == nil && sharedDeliveryFile(filepath.Dir(path), receipt.TaskID, receipt.Namespace) != path {
		return receipt, errors.New("shared delivery receipt identity mismatch")
	}
	receipt.FilePath = path
	return receipt, err
}

func resolveSharedWorktreeReceipts(dir, commit string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "delivery-") {
			continue
		}
		receipt, err := readSharedDelivery(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if receipt.Commit != "" || receipt.NoWork {
			continue
		}
		if commit == "" {
			receipt.NoWork = true
			receipt.Branch = ""
		}
		receipt.Commit = commit
		if err := preserveSharedWorktreeReceipt(dir, receipt); err != nil {
			return err
		}
	}
	return nil
}

// PendingSharedWorktreeDeliveries returns settled receipts only from this
// report namespace; other profiles and accounts remain untouched.
func PendingSharedWorktreeDeliveries(ctx context.Context, namespace string) ([]SharedWorktreeDelivery, error) {
	if namespace == "" {
		return nil, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(cache, "multica", "shared-directories")
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipts []SharedWorktreeDelivery
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		state := filepath.Join(root, dir.Name())
		candidates, err := os.ReadDir(state)
		if err != nil {
			return receipts, err
		}
		owned := false
		for _, candidate := range candidates {
			if strings.HasPrefix(candidate.Name(), sharedDeliveryPrefix(namespace)) {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		unlock, err := lockSharedDirectoryState(ctx, state)
		if err != nil {
			return receipts, err
		}
		entries, err := os.ReadDir(state)
		if err != nil {
			unlock()
			return receipts, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), sharedDeliveryPrefix(namespace)) {
				continue
			}
			receipt, err := readSharedDelivery(filepath.Join(state, entry.Name()))
			if err != nil {
				unlock()
				return receipts, err
			}
			if receipt.Namespace == namespace && (receipt.Commit != "" || receipt.NoWork) {
				receipts = append(receipts, receipt)
			}
		}
		unlock()
	}
	return receipts, nil
}

// AcknowledgeSharedWorktreeDelivery retires the exact accepted generation.
func AcknowledgeSharedWorktreeDelivery(ctx context.Context, receipt SharedWorktreeDelivery) error {
	return AcknowledgeSharedWorktreeDeliveryAt(ctx, receipt.FilePath, receipt)
}

// AcknowledgeSharedWorktreeDeliveryAt retires the receipt stored at path, after
// confirming the on-disk generation still matches the accepted receipt. It is
// the path-explicit form used by the environment service so a cross-process
// caller can retire a receipt without serializing the unexported file handle;
// the exact-generation comparison is identical to the single-process form.
func AcknowledgeSharedWorktreeDeliveryAt(ctx context.Context, path string, receipt SharedWorktreeDelivery) error {
	unlock, err := lockSharedDirectoryState(ctx, filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readSharedDelivery(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Commit != receipt.Commit || current.NoWork != receipt.NoWork || current.Namespace != receipt.Namespace || current.TaskID != receipt.TaskID {
		return errors.New("shared delivery receipt changed")
	}
	return os.Remove(path)
}
