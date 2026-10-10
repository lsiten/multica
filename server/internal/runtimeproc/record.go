package runtimeproc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Bootstrap is passed only through private stdin, never through argv or logs.
// Root is caller-selected storage; records cannot redirect it to another path.
type Bootstrap struct {
	Root     string   `json:"root"`
	Identity Identity `json:"identity"`
	Token    string   `json:"token"`
	Fence    Fence    `json:"fence"`
}

// Record stores credentials and receipts under a single kernel-locked owner.
type Record struct {
	Reconciliation json.RawMessage    `json:"reconciliation,omitempty"`
	ReplayEpoch    uint64             `json:"replay_epoch"`
	Identity       Identity           `json:"identity"`
	Token          string             `json:"token"`
	Address        string             `json:"address"`
	State          string             `json:"state"`
	Fence          Fence              `json:"fence"`
	Operations     map[string]Receipt `json:"operations"`
}

// NewBootstrap creates a private per-process control credential.
func NewBootstrap(root string, identity Identity) (Bootstrap, error) {
	token, err := randomHex(32)
	if err != nil {
		return Bootstrap{}, err
	}
	b := Bootstrap{Root: root, Identity: identity, Token: token, Fence: Fence{1, 1, 1}}
	return b, b.validate()
}
func (b Bootstrap) validate() error {
	if !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root {
		return errors.New("runtime root must be an absolute clean path")
	}
	if err := b.Identity.Validate(); err != nil {
		return err
	}
	if len(b.Token) != 64 {
		return errors.New("invalid bootstrap credential")
	}
	if _, err := hex.DecodeString(b.Token); err != nil {
		return errors.New("invalid bootstrap credential")
	}
	if b.Fence.SupervisorEpoch == 0 || b.Fence.ResourceEpoch == 0 || b.Fence.Revision == 0 {
		return errors.New("invalid runtime fence")
	}
	return nil
}
func scopeDirectory(root string, scope Scope) string {
	raw, _ := json.Marshal(scope)
	sum := sha256.Sum256(raw)
	return filepath.Join(root, hex.EncodeToString(sum[:]))
}

// RecordPath derives the only accepted record location from caller-owned scope.
func RecordPath(root string, scope Scope) string {
	return filepath.Join(scopeDirectory(root, scope), "owner.json")
}

// PrepareRoot creates private storage only when absent; existing storage must
// already satisfy owner, permissions and canonical-path checks.
func PrepareRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("invalid runtime root")
	}
	// Canonical ancestors prevent redirection through an intermediate symlink.
	resolved, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		if err = secureNewDirectory(root); err != nil {
			return err
		}
		resolved, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return err
	}
	if resolved != root {
		return errors.New("runtime root contains a symlink")
	}
	if err = checkDirectory(root); err != nil {
		return err
	}
	return nil
}

func prepareDirectory(root string, scope Scope) (string, error) {
	if err := PrepareRoot(root); err != nil {
		return "", err
	}
	var err error
	dir := scopeDirectory(root, scope)
	if err = os.Mkdir(dir, 0700); err == nil {
		err = secureNewDirectory(dir)
	} else if errors.Is(err, os.ErrExist) {
		err = checkDirectory(dir)
	}
	if err != nil {
		return "", err
	}
	return dir, nil
}
func checkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime storage must be a private directory")
	}
	return validateDirectory(path)
}
func readPrivate(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("runtime record is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("runtime record changed during opening")
	}
	if err = validatePrivateFile(f); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxJournal+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxJournal {
		return nil, errors.New("runtime record quota exceeded")
	}
	return raw, nil
}

// ReadRecord requires caller-supplied scope and never trusts a path inside a record.
func ReadRecord(root string, expected Identity) (Record, error) {
	var r Record
	if err := expected.Validate(); err != nil {
		return r, err
	}
	if err := validateStorage(root, expected.Scope); err != nil {
		return r, err
	}
	raw, err := readPrivate(RecordPath(root, expected.Scope))
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, errors.New("invalid runtime record")
	}
	if r.Identity != expected {
		return r, errors.New("runtime record identity mismatch")
	}
	if err = (Bootstrap{Root: root, Identity: r.Identity, Token: r.Token, Fence: r.Fence}).validate(); err != nil {
		return r, err
	}
	if r.Address != "" {
		if err = validateOrigin(r.Address); err != nil {
			return r, err
		}
	}
	switch r.State {
	case "starting", "ready", "draining", "stopped", "suspect":
	default:
		return r, errors.New("invalid runtime state")
	}
	if r.ReplayEpoch == 0 {
		return r, errors.New("invalid replay epoch")
	}
	if len(r.Operations) > maxOperations+17 {
		return r, errors.New("runtime receipt quota exceeded")
	}
	return r, nil
}
func writeRecord(path string, r Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(raw) > maxJournal {
		return errors.New("runtime journal quota exceeded")
	}
	if _, err = os.Lstat(path); err == nil {
		if _, err = readPrivate(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = protectPrivateFile(f); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = replaceFile(f.Name(), path); err != nil {
		return fmt.Errorf("persist runtime receipt: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func validateStorage(root string, scope Scope) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("invalid runtime root")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if canonical != root {
		return errors.New("runtime root contains a symlink")
	}
	if err = checkDirectory(root); err != nil {
		return err
	}
	return checkDirectory(scopeDirectory(root, scope))
}
func replaceableRecord(raw []byte, identity Identity) bool {
	var previous Record
	if json.Unmarshal(raw, &previous) != nil || previous.Identity.Scope != identity.Scope || previous.Identity.InstanceID == identity.InstanceID || previous.Identity.Validate() != nil || previous.State != "stopped" {
		return false
	}
	for _, receipt := range previous.Operations {
		if receipt.State != "completed" {
			return false
		}
	}
	return true
}
