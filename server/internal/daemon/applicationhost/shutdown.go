package applicationhost

import (
	"context"
	"errors"
	"os"
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
			current, readErr := ReadRecord(recordPath)
			closeErr := lock.Close()
			if readErr != nil {
				return errors.Join(readErr, closeErr)
			}
			if current.HostID != hostID || current.Observation.ProcessState != "stopped" {
				return errors.Join(errors.New("application shutdown ownership changed"), closeErr)
			}
			return closeErr
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

// WithRecordOwnership serializes a mutation of an exact private record against
// the kernel host owner. An empty expectedHostID requires an absent record.
// Acquiring an unlocked file alone is not evidence of stopped processes: the
// caller must separately validate a stopped receipt, prior boot, or its own
// known-not-started launch before mutating the record.
func WithRecordOwnership(recordPath, expectedHostID string, mutate func(Record) error) (returnErr error) {
	lock, err := lockHost(filepath.Join(filepath.Dir(recordPath), "host.lock"))
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	record, err := ReadRecord(recordPath)
	if errors.Is(err, os.ErrNotExist) && expectedHostID == "" {
		return mutate(Record{})
	}
	if err != nil {
		return err
	}
	if record.HostID != expectedHostID {
		return errors.New("application host record ownership changed")
	}
	return mutate(record)
}
