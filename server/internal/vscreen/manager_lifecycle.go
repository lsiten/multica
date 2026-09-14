package vscreen

import (
	"context"
	"errors"
)

func (m *Manager) revokeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, a := range m.actors {
		a.mu.Lock()
		a.closed = true
		a.freezeLocked()
		a.mu.Unlock()
	}
}

// Close is the daemon shutdown seam. It revokes every runtime before waiting for
// any native cleanup. Native failures retain their frozen actors and display state.
func (m *Manager) Close(ctx context.Context) error {
	m.revokeAll()
	m.mu.Lock()
	actors := make([]*Actor, 0, len(m.actors))
	for _, a := range m.actors {
		actors = append(actors, a)
	}
	m.mu.Unlock()
	var failures []error
	for _, a := range actors {
		if err := a.Dispose(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
