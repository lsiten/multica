package jevmodels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	RootDir      string
	PythonPath   string
	ReadyTimeout time.Duration
	IdleTimeout  time.Duration
}

type Status struct {
	ModelID         string `json:"model_id"`
	Revision        string `json:"revision"`
	State           string `json:"state"`
	Phase           string `json:"phase,omitempty"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Device          string `json:"device,omitempty"`
	ActiveLeases    int    `json:"active_leases"`
	Error           string `json:"error,omitempty"`
}

type Manager struct {
	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
	cfg               Config
	status            Status
	installCancel     context.CancelFunc
	installDone       chan struct{}
	installQueued     bool
	installCancelled  bool
	installGeneration uint64
	installWG         sync.WaitGroup
	installWorkers    int
	proc              *modelProcess
	starting          chan struct{}
	idle              *time.Timer
	closed            bool
	installed         bool
	lock              *os.File
}

// New only inspects the cache. It never installs dependencies or downloads weights.
// RootDir must be daemon-profile storage, never a task's resettable directory.
func New(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.RootDir == "" || !filepath.IsAbs(cfg.RootDir) {
		return nil, errors.New("absolute daemon model cache directory required")
	}
	if cfg.PythonPath == "" {
		cfg.PythonPath = "python3"
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 3 * time.Minute
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if err := os.MkdirAll(cfg.RootDir, 0700); err != nil {
		return nil, err
	}
	lock, err := lockCache(filepath.Join(cfg.RootDir, "manager.lock"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{lock: lock, ctx: ctx, cancel: cancel, cfg: cfg, status: Status{ModelID: ModelID, Revision: Revision, State: "not_installed", TotalBytes: Catalog()[0].DownloadBytes}}
	if _, err := os.Stat(filepath.Join(m.modelDir(), "installed.json")); err == nil {
		if err = verifyFiles(ctx, m.modelDir(), modelFiles); err == nil {
			m.installed = true
			m.status.State = "installed"
			m.status.DownloadedBytes = m.status.TotalBytes
		} else {
			m.status.State = "failed"
			m.status.Error = err.Error()
		}
	}
	return m, nil
}

func (m *Manager) modelDir() string { return filepath.Join(m.cfg.RootDir, Revision) }

func (m *Manager) Status(id string) (Status, error) {
	if id != ModelID {
		return Status{}, ErrUnknownModel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detectExitLocked()
	return m.status, nil
}

func (m *Manager) CancelInstall(id string) error {
	if id != ModelID {
		return ErrUnknownModel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installQueued && m.installCancel == nil {
		m.installGeneration++
		m.installQueued = false
		m.installCancelled = true
		m.status.State = "not_installed"
		m.status.Phase = ""
		m.status.Error = "download cancelled before start"
		return nil
	}
	if m.installCancel != nil {
		// Invalidate the active generation before cancelling so its finalizer
		// cannot overwrite a subsequent retry's status.
		m.installGeneration++
		cancel := m.installCancel
		m.installCancel = nil
		cancel()
		m.status.State = "not_installed"
		m.status.Phase = ""
		m.status.Error = "download cancelled"
	}
	return nil
}

func (m *Manager) Stop(id string) error {
	if id != ModelID {
		return ErrUnknownModel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.starting != nil || m.status.ActiveLeases > 0 || m.installWorkers > 0 || m.installCancel != nil || m.installQueued {
		return ErrBusy
	}
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	if m.idle != nil {
		m.idle.Stop()
		m.idle = nil
	}
	if m.proc == nil {
		return nil
	}
	m.status.State = "stopping"
	err := m.proc.stop()
	m.proc = nil
	m.status.State = "stopped"
	m.status.Device = ""
	return err
}

func (m *Manager) Remove(id string) error {
	if id != ModelID {
		return ErrUnknownModel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.starting != nil || m.status.ActiveLeases > 0 || m.installWorkers > 0 || m.installCancel != nil || m.installQueued {
		return ErrBusy
	}
	if err := m.stopLocked(); err != nil {
		return err
	}
	if err := os.RemoveAll(m.modelDir()); err != nil {
		return err
	}
	m.installed = false
	m.status.State = "not_installed"
	m.status.DownloadedBytes = 0
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	if m.installCancel != nil {
		m.installCancel()
	}
	m.installQueued = false
	starting := m.starting
	m.mu.Unlock()
	if starting != nil {
		<-starting
	}
	m.installWG.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.stopLocked()
	if m.lock != nil {
		err = errors.Join(err, m.lock.Close())
		m.lock = nil
	}
	return err
}

// StartInstall atomically reserves an explicit model installation and starts it.
// The returned channel receives exactly one result. A reservation generation
// prevents a cancelled worker from completing a later retry's job.
func (m *Manager) StartInstall(ctx context.Context, id string) (<-chan error, error) {
	if id != ModelID {
		return nil, ErrUnknownModel
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if m.installed {
		result := make(chan error, 1)
		result <- nil
		close(result)
		m.mu.Unlock()
		return result, nil
	}
	if m.installQueued || m.installCancel != nil || m.starting != nil || m.proc != nil {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	m.installGeneration++
	generation := m.installGeneration
	m.installQueued = true
	m.installCancelled = false
	m.status.State = "queued"
	m.status.Phase = "queued"
	m.status.Error = ""
	result := make(chan error, 1)
	m.installWG.Add(1)
	m.installWorkers++
	m.mu.Unlock()
	go func() {
		defer m.installWG.Done()
		defer func() { m.mu.Lock(); m.installWorkers--; m.mu.Unlock() }()
		err := m.runInstall(ctx, id, generation)
		result <- err
		close(result)
	}()
	return result, nil
}

func (m *Manager) runInstall(ctx context.Context, id string, generation uint64) error {
	m.mu.Lock()
	if m.closed || generation != m.installGeneration || !m.installQueued || m.installCancelled {
		m.mu.Unlock()
		return context.Canceled
	}
	m.installQueued = false
	installCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	m.installCancel = cancel
	m.installDone = make(chan struct{})
	m.status.State = "downloading"
	m.status.Phase = "dependencies"
	m.status.Error = ""
	m.status.DownloadedBytes = 0
	m.mu.Unlock()
	err := m.installBody(installCtx, generation)
	if installCtx.Err() != nil {
		err = installCtx.Err()
	}
	stop()
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation == m.installGeneration {
		m.installCancel = nil
		if m.installDone != nil {
			close(m.installDone)
			m.installDone = nil
		}
		if err != nil {
			m.status.State = "failed"
			m.status.Error = err.Error()
		} else {
			m.installed = true
			m.status.State = "installed"
			m.status.Phase = ""
		}
	}
	return err
}
