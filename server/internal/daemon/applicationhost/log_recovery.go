package applicationhost

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const logBacklogLimit = 4 << 20

func (l *boundedLog) open() error {
	before, err := os.Lstat(l.path)
	if err == nil && !before.Mode().IsRegular() {
		return errUnsafeLog
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	if !info.Mode().IsRegular() || before != nil && !os.SameFile(before, info) {
		file.Close()
		return errUnsafeLog
	}
	if err := protectPrivateFile(file); err != nil {
		file.Close()
		return fmt.Errorf("%w: %v", errUnsafeLog, err)
	}
	if err := validatePrivateFile(file); err != nil {
		file.Close()
		return fmt.Errorf("%w: %v", errUnsafeLog, err)
	}
	l.file, l.size = file, info.Size()
	return nil
}

func (l *boundedLog) buffer(redacted []byte) {
	available := max(0, logBacklogLimit-len(l.backlog))
	retained := min(available, len(redacted))
	l.backlog = append(l.backlog, redacted[:retained]...)
	if retained < len(redacted) {
		l.dropped += uint64(len(redacted) - retained)
		l.deferWrite(errors.New("application log backlog capacity exhausted"))
	}
}

func (l *boundedLog) deferWrite(err error) {
	if l.failure == nil || time.Now().After(l.retryAt) {
		slog.Warn("application log storage unavailable; business process continues", "host_id", l.hostID, "pending_bytes", len(l.backlog), "dropped_bytes", l.dropped)
	}
	l.failure = err
	l.retryAt = time.Now().Add(5 * time.Second)
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

func (l *boundedLog) retry() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil && !time.Now().Before(l.retryAt) {
		if l.restorePending {
			if err := l.restoreBacklog(); err != nil {
				l.deferWrite(err)
				return
			}
			l.restorePending = false
		}
		if err := l.flush(false); err != nil {
			l.deferWrite(err)
		}
		if err := l.saveBacklog(); err != nil {
			l.deferWrite(err)
		}
	}
}

func (l *boundedLog) restoreBacklog() error {
	file, _, err := openStoredLog(l.path + ".pending")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending application log: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, logBacklogLimit+1))
	if err != nil || len(raw) > logBacklogLimit {
		return errors.New("pending application log exceeds bounded storage")
	}
	current := l.backlog
	l.backlog = raw
	l.buffer(current)
	return nil
}

func (l *boundedLog) saveBacklog() error {
	if l.restorePending {
		return errors.New("pending application log could not be read; preserving its existing bytes")
	}
	path := l.path + ".pending"
	if len(l.backlog) == 0 {
		if l.file != nil {
			if err := l.file.Sync(); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".log-pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectPrivateFile(file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(l.backlog); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
