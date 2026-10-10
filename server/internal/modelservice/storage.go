package modelservice

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// InstallJob is domain recovery state; a new attempt always receives a new ID.
type InstallJob struct {
	ID        string    `json:"id"`
	ModelID   string    `json:"model_id"`
	Revision  string    `json:"revision"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}
type inventory struct {
	InstanceID          string                `json:"instance_id"`
	DescendantsPossible bool                  `json:"descendants_possible"`
	Jobs                map[string]InstallJob `json:"jobs"`
}

func readInventory(root string) (inventory, error) {
	var inv inventory
	path := filepath.Join(root, "model-service.json")
	info, err := os.Lstat(path)
	if err != nil {
		return inv, err
	}
	if !info.Mode().IsRegular() {
		return inv, errors.New("model service inventory must be regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return inv, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) {
		return inv, errors.New("model inventory changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return inv, err
	}
	if len(raw) > 1<<20 {
		return inv, errors.New("model inventory exceeds limit")
	}
	if err = json.Unmarshal(raw, &inv); err != nil {
		return inv, err
	}
	if len(inv.Jobs) > 65 {
		return inv, errors.New("too many model install jobs")
	}
	return inv, nil
}
func saveInventory(root string, inv inventory) error {
	raw, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("model inventory exceeds limit")
	}
	path := filepath.Join(root, "model-service.json")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("model inventory must be regular")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(root, ".model-inventory-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	if err = replaceStateFile(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func activeJob(state string) bool {
	return state == "queued" || state == "downloading" || state == "verifying"
}
