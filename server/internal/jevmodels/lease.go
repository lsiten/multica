package jevmodels

import (
	"context"
	"sync"
	"time"
)

type Lease struct {
	Endpoint          string
	Token             string
	Device            string
	ExecutionIdentity string
	manager           *Manager
	process           *modelProcess
	once              sync.Once
}

// Acquire never installs anything. Concurrent tasks share one ready host process.
func (m *Manager) Acquire(ctx context.Context, s Selection) (*Lease, error) {
	if !m.matches(s.ModelID, []string{s.Revision}) {
		child, err := m.child(s.ModelID, []string{s.Revision})
		if err != nil {
			return nil, err
		}
		return child.Acquire(ctx, s)
	}
	if s.Device == "" {
		s.Device = "auto"
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		if !m.installed {
			m.mu.Unlock()
			return nil, ErrNotInstalled
		}
		if done := m.starting; done != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		m.detectExitLocked()
		if m.proc != nil {
			if m.status.State == "failed" || m.proc.requestedDevice != s.Device {
				if m.status.ActiveLeases > 0 {
					m.mu.Unlock()
					return nil, ErrBusy
				}
				if err := m.stopLocked(); err != nil {
					m.mu.Unlock()
					return nil, err
				}
			} else {
				if m.idle != nil {
					m.idle.Stop()
					m.idle = nil
				}
				m.status.ActiveLeases++
				lease := &Lease{Endpoint: m.proc.endpoint, Token: m.proc.token, Device: m.proc.device, ExecutionIdentity: s.ExecutionIdentity, manager: m, process: m.proc}
				m.mu.Unlock()
				return lease, nil
			}
		}
		m.starting = make(chan struct{})
		m.status.State = "starting"
		m.status.Error = ""
		m.mu.Unlock()
		startCtx, startCancel := context.WithCancel(ctx)
		stopCancel := context.AfterFunc(m.ctx, startCancel)
		proc, err := m.start(startCtx, s)
		stopCancel()
		startCancel()
		m.mu.Lock()
		if err != nil {
			m.status.State = "failed"
			m.status.Error = err.Error()
		} else {
			m.proc = proc
			m.status.State = "ready"
			m.status.Device = proc.device
			m.scheduleIdleLocked(proc)
		}
		close(m.starting)
		m.starting = nil
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
}

func (l *Lease) Release() {
	l.once.Do(func() {
		m := l.manager
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.proc != l.process {
			return
		}
		m.status.ActiveLeases--
		if m.status.ActiveLeases == 0 && !m.closed {
			m.scheduleIdleLocked(l.process)
		}
	})
}

func (m *Manager) scheduleIdleLocked(proc *modelProcess) {
	if m.idle != nil {
		m.idle.Stop()
	}
	m.idle = time.AfterFunc(m.cfg.IdleTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.proc == proc && m.status.ActiveLeases == 0 {
			m.stopLocked()
		}
	})
}
