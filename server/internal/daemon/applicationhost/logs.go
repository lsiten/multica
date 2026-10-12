package applicationhost

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogPage has a resumable cursor and explicitly signals rotation gaps.
type LogPage struct {
	Text   string `json:"text"`
	Cursor string `json:"cursor"`
	Gap    bool   `json:"gap"`
}

type boundedLog struct {
	mu             sync.Mutex
	path           string
	hostID         string
	file           *os.File
	limit          int64
	size           int64
	rotation       uint64
	secrets        []string
	pending        []byte
	backlog        []byte
	retryAt        time.Time
	failure        error
	dropped        uint64
	restorePending bool
}

var errUnsafeLog = errors.New("application log ownership is unsafe")

func openLog(path, hostID string, limit int64, secrets []string) (*boundedLog, error) {
	if limit < 1 {
		return nil, errors.New("application log size limit must be positive")
	}
	log := &boundedLog{path: path, hostID: hostID, limit: limit}
	for _, secret := range secrets {
		if secret != "" {
			log.secrets = append(log.secrets, secret)
		}
	}
	sort.Slice(log.secrets, func(i, j int) bool { return len(log.secrets[i]) > len(log.secrets[j]) })
	if err := log.open(); err != nil {
		if errors.Is(err, errUnsafeLog) {
			return nil, err
		}
		log.deferWrite(err)
	}
	if err := log.restoreBacklog(); err != nil {
		if errors.Is(err, errUnsafeLog) {
			if log.file != nil {
				_ = log.file.Close()
			}
			return nil, err
		}
		log.restorePending = true
		log.deferWrite(err)
	}
	return log, nil
}

func (l *boundedLog) Write(raw []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = append(l.pending, raw...)
	output := make([]byte, 0, len(l.pending))
	consumed := 0
scan:
	for consumed < len(l.pending) {
		matched := false
		for _, secret := range l.secrets {
			// Keep only a possible credential prefix, including longer overlaps.
			if len(l.pending)-consumed < len(secret) && bytes.HasPrefix([]byte(secret), l.pending[consumed:]) {
				break scan
			}
			if bytes.HasPrefix(l.pending[consumed:], []byte(secret)) {
				output = append(output, "[redacted]"...)
				consumed += len(secret)
				matched = true
				break
			}
		}
		if !matched {
			output = append(output, l.pending[consumed])
			consumed++
		}
	}
	l.pending = append(l.pending[:0], l.pending[consumed:]...)
	l.buffer(output)
	if err := l.flush(false); err != nil {
		l.deferWrite(err)
	}
	return len(raw), nil
}

func (l *boundedLog) writeRedacted(raw []byte) error {
	text := string(raw)
	for _, secret := range l.secrets {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	l.buffer([]byte(text))
	return l.flush(true)
}

func (l *boundedLog) flush(force bool) error {
	if !force && time.Now().Before(l.retryAt) {
		return nil
	}
	if l.file == nil {
		if err := l.open(); err != nil {
			return err
		}
	}
	for len(l.backlog) > 0 {
		if l.size >= l.limit {
			if err := l.rotate(); err != nil {
				return err
			}
		}
		amount := min(int64(len(l.backlog)), l.limit-l.size)
		written, err := l.file.Write(l.backlog[:amount])
		l.size += int64(written)
		l.backlog = l.backlog[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	l.failure = nil
	l.retryAt = time.Time{}
	return nil
}

func (l *boundedLog) rotate() error {
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return err
	}
	for number := 2; number >= 1; number-- {
		source := l.path
		if number > 1 {
			source = l.path + "." + strconv.Itoa(number-1)
		}
		if err := os.Rename(source, l.path+"."+strconv.Itoa(number)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = protectPrivateFile(file); err != nil {
		file.Close()
		return err
	}
	l.file = file
	l.size = 0
	l.rotation++
	return nil
}

func (l *boundedLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.writeRedacted(l.pending)
	l.pending = nil
	if err != nil {
		l.deferWrite(err)
		l.retryAt = time.Time{}
		if err = l.flush(true); err != nil {
			l.deferWrite(err)
		}
	}
	if err := l.saveBacklog(); err != nil {
		l.deferWrite(err)
	}
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			l.deferWrite(err)
		}
		l.file = nil
	}
	return nil
}

func (l *boundedLog) page(cursor string, limit int) (LogPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit < 1 || limit > 65536 {
		return LogPage{}, errors.New("log limit must be between 1 and 65536 bytes")
	}
	offset := int64(0)
	gap := false
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 3 {
			return LogPage{}, errors.New("invalid application log cursor")
		}
		rotation, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return LogPage{}, errors.New("invalid application log cursor")
		}
		offset, err = strconv.ParseInt(parts[2], 10, 64)
		if err != nil || offset < 0 {
			return LogPage{}, errors.New("invalid application log cursor")
		}
		if parts[0] != l.hostID || rotation != l.rotation || offset > l.size {
			gap = true
			offset = 0
		}
	}
	file, _, err := openStoredLog(l.path)
	if err != nil {
		return LogPage{}, err
	}
	defer file.Close()
	amount := min(int64(limit), l.size-offset)
	raw := make([]byte, amount)
	if amount > 0 {
		if _, err = file.ReadAt(raw, offset); err != nil {
			return LogPage{}, err
		}
	}
	return LogPage{Text: strings.ToValidUTF8(string(raw), "�"), Cursor: fmt.Sprintf("%s:%d:%d", l.hostID, l.rotation, offset+amount), Gap: gap}, nil
}

func openStoredLog(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errUnsafeLog
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		file.Close()
		return nil, nil, fmt.Errorf("%w: log changed while opening", errUnsafeLog)
	}
	if err := validatePrivateFile(file); err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("%w: %v", errUnsafeLog, err)
	}
	return file, info, nil
}

// ReadStoredLogs reads redacted local output after the service host has exited.
func ReadStoredLogs(recordPath, cursor string, limit int) (LogPage, error) {
	record, err := ReadRecord(recordPath)
	if err != nil {
		return LogPage{}, err
	}
	path := filepath.Join(filepath.Dir(recordPath), "service.log")
	if _, err := os.Lstat(path + ".pending"); err == nil {
		if err := WithStoppedOwnership(recordPath, record.HostID, func(current Record) error {
			log, err := openLog(path, current.HostID, 4<<20, nil)
			if err != nil {
				return err
			}
			log.rotation = current.LogRotation
			if err := log.close(); err != nil {
				return err
			}
			current.LogRotation = log.rotation
			current.LogPendingBytes = len(log.backlog)
			current.LogDroppedBytes += log.dropped
			if log.failure == nil {
				current.LogError = ""
			} else {
				current.LogError = "log_storage_unavailable"
			}
			record = current
			return WriteRecord(recordPath, current)
		}); err != nil {
			return LogPage{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LogPage{}, err
	}
	file, info, err := openStoredLog(path)
	if err != nil {
		return LogPage{}, err
	}
	if err := file.Close(); err != nil {
		return LogPage{}, err
	}
	log := &boundedLog{path: path, hostID: record.HostID, rotation: record.LogRotation, size: info.Size()}
	return log.page(cursor, limit)
}
