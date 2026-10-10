package jevmodels

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/daemon/processtree"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	PersistInstall bool
	RootDir        string
	PythonPath     string
	ReadyTimeout   time.Duration
	IdleTimeout    time.Duration
	modelSpec      *modelSpec
	engineRoot     string
	engineMu       *sync.Mutex
}

type Status struct {
	Installed       bool   `json:"installed"`
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
	closeMu           sync.Mutex
	catalogMu         sync.Mutex
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
	cleanupErr        error
	installed         bool
	lock              *os.File
	model             Model
	files             []modelFile
	registered        map[string]modelSpec
	children          map[string]*Manager
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
	model, files := Catalog()[0], modelFiles
	if cfg.modelSpec != nil {
		model, files = cfg.modelSpec.Model, cfg.modelSpec.Files
	}
	if cfg.engineRoot == "" {
		cfg.engineRoot = cfg.RootDir
	}
	if cfg.engineMu == nil {
		cfg.engineMu = &sync.Mutex{}
	}
	m := &Manager{lock: lock, ctx: ctx, cancel: cancel, cfg: cfg, model: model, files: files, registered: map[string]modelSpec{}, children: map[string]*Manager{}, status: Status{ModelID: model.ID, Revision: model.Revision, State: "not_installed", TotalBytes: model.DownloadBytes}}
	if cfg.modelSpec == nil {
		if err := m.loadCatalog(); err != nil {
			cancel()
			lock.Close()
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(m.modelDir(), "installed.json")); err == nil {
		if err = verifyFiles(ctx, m.modelDir(), m.files); err == nil {
			m.installed = true
			m.status.State = "installed"
			m.status.DownloadedBytes = m.status.TotalBytes
		} else {
			m.status.State = "failed"
			m.status.Error = err.Error()
		}
	}
	if err := m.loadInstallState(); err != nil {
		cancel()
		lock.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) modelDir() string { return filepath.Join(m.cfg.RootDir, m.model.Revision) }

func (m *Manager) Status(id string, revisions ...string) (Status, error) {
	if !m.matches(id, revisions) {
		child, err := m.child(id, revisions)
		if err != nil {
			return Status{}, err
		}
		return child.Status(id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detectExitLocked()
	status := m.status
	status.Installed = m.installed
	return status, nil
}

func (m *Manager) CancelInstall(id string, revisions ...string) error {
	if !m.matches(id, revisions) {
		child, err := m.child(id, revisions)
		if err != nil {
			return err
		}
		return child.CancelInstall(id)
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
		return m.persistInstallLocked("cancelled")
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
		return m.persistInstallLocked("cancelled")
	}
	return nil
}

func (m *Manager) Stop(id string, revisions ...string) error {
	if !m.matches(id, revisions) {
		child, err := m.child(id, revisions)
		if err != nil {
			return err
		}
		return child.Stop(id)
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
	if errors.Is(err, processtree.ErrCleanup) {
		m.cleanupErr = errors.Join(m.cleanupErr, err)
	}
	m.proc = nil
	m.status.State = "stopped"
	m.status.Device = ""
	return err
}

func (m *Manager) Remove(id string, revisions ...string) error {
	if !m.matches(id, revisions) {
		child, err := m.child(id, revisions)
		if err != nil {
			return err
		}
		return child.Remove(id)
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
	return m.persistInstallLocked("")
}

func (m *Manager) Close() error { return m.CloseWithReceipt(nil) }

// CloseWithReceipt confirms descendant cleanup while retaining the cache ownership
// lock until the caller has durably recorded its domain shutdown receipt.
func (m *Manager) CloseWithReceipt(receipt func() error) error {
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	m.mu.Lock()
	m.closed = true
	m.cancel()
	if m.installCancel != nil {
		m.installCancel()
	}
	m.installQueued = false
	starting := m.starting
	m.mu.Unlock()
	m.catalogMu.Lock()
	children := make([]*Manager, 0, len(m.children))
	for _, child := range m.children {
		children = append(children, child)
	}
	m.catalogMu.Unlock()
	if starting != nil {
		<-starting
	}
	m.installWG.Wait()
	var childErr error
	for _, child := range children {
		childErr = errors.Join(childErr, child.Close())
	}
	m.mu.Lock()
	err := errors.Join(childErr, m.stopLocked(), m.cleanupErr)
	m.mu.Unlock()
	if err == nil && receipt != nil {
		err = receipt()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock != nil {
		err = errors.Join(err, m.lock.Close())
		m.lock = nil
	}
	return err
}

// StartInstall atomically reserves an explicit model installation and starts it.
// The returned channel receives exactly one result. A reservation generation
// prevents a cancelled worker from completing a later retry's job.
func (m *Manager) StartInstall(ctx context.Context, id string, revisions ...string) (<-chan error, error) {
	if !m.matches(id, revisions) {
		child, err := m.child(id, revisions)
		if err != nil {
			return nil, err
		}
		return child.StartInstall(ctx, id)
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
	if err := m.persistInstallLocked(""); err != nil {
		m.installQueued = false
		m.status.State = "failed"
		m.status.Error = err.Error()
		m.mu.Unlock()
		return nil, err
	}
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
	persistErr := m.persistInstallLocked("")
	m.mu.Unlock()
	var err error
	if persistErr != nil {
		err = persistErr
	} else {
		err = m.installBody(installCtx, generation)
	}
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
		err = errors.Join(err, m.persistInstallLocked(""))
	}
	return err
}
