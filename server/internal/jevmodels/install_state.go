package jevmodels

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// persistInstallLocked records only install lifecycle, never a resumable download.
// The generation prevents a canceled job from publishing a later attempt's state.
func (m *Manager) persistInstallLocked(state string) error {
	if !m.cfg.PersistInstall {
		return nil
	}
	status := m.status
	if state != "" {
		status.State = state
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	path := filepath.Join(m.cfg.RootDir, "install-state.json")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("install state must be regular")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(m.cfg.RootDir, ".install-state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = replaceStateFile(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(m.cfg.RootDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (m *Manager) loadInstallState() error {
	if !m.cfg.PersistInstall || m.installed {
		return nil
	}
	path := filepath.Join(m.cfg.RootDir, "install-state.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("install state must be regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) {
		return errors.New("install state changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return err
	}
	if len(raw) > 16<<10 {
		return errors.New("install state exceeds limit")
	}
	var status Status
	if err = json.Unmarshal(raw, &status); err != nil {
		return err
	}
	if status.ModelID != m.model.ID || status.Revision != m.model.Revision {
		return errors.New("install state belongs to another model revision")
	}
	if status.State == "installed" {
		status.State = "failed"
		status.Error = "verified model files are missing"
	}
	switch status.State {
	case "queued", "downloading", "verifying":
		status.State = "interrupted"
		status.Error = "installation interrupted; explicit retry required"
	}
	m.status = status
	m.status.Installed = false
	return m.persistInstallLocked("")
}
