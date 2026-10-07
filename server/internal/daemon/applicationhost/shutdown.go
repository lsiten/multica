package applicationhost

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// WaitStopped waits until the host has finished persisting state and released its local ownership lock.
func WaitStopped(ctx context.Context, recordPath, hostID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := ReadRecord(recordPath)
		if err != nil {
			return err
		}
		if record.HostID != hostID || record.Observation.ProcessState != "stopped" {
			return errors.New("application shutdown ownership changed")
		}
		lock, err := lockHost(filepath.Join(filepath.Dir(recordPath), "host.lock"))
		if err == nil {
			return lock.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// WithStoppedOwnership protects retained-file cleanup against a live application host.
func WithStoppedOwnership(recordPath, hostID string, cleanup func(Record) error) error {
	lock, err := lockHost(filepath.Join(filepath.Dir(recordPath), "host.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	record, err := ReadRecord(recordPath)
	if err != nil {
		return err
	}
	if record.HostID != hostID || record.Observation.ProcessState != "stopped" {
		return errors.New("application ownership is not confirmed stopped")
	}
	return cleanup(record)
}
