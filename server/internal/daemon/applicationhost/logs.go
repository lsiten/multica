package applicationhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// LogPage has a resumable cursor and explicitly signals rotation gaps.
type LogPage struct {
	Text   string `json:"text"`
	Cursor string `json:"cursor"`
	Gap    bool   `json:"gap"`
}

type boundedLog struct {
	mu       sync.Mutex
	path     string
	hostID   string
	file     *os.File
	limit    int64
	size     int64
	rotation uint64
	secrets  []string
	pending  []byte
}

func openLog(path, hostID string, limit int64, secrets []string) (*boundedLog, error) {
	if limit < 1 {
		return nil, errors.New("application log size limit must be positive")
	}
	if existing, err := os.Lstat(path); err == nil && !existing.Mode().IsRegular() {
		return nil, errors.New("application log must be a private regular file")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = protectPrivateFile(file); err != nil {
		file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	log := &boundedLog{path: path, hostID: hostID, file: file, limit: limit, size: info.Size()}
	for _, secret := range secrets {
		if secret != "" {
			log.secrets = append(log.secrets, secret)
		}
	}
	sort.Slice(log.secrets, func(i, j int) bool { return len(log.secrets[i]) > len(log.secrets[j]) })
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
	if err := l.writeBytes(output); err != nil {
		return 0, err
	}
	l.pending = append(l.pending[:0], l.pending[consumed:]...)
	return len(raw), nil
}

func (l *boundedLog) writeRedacted(raw []byte) error {
	text := string(raw)
	for _, secret := range l.secrets {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	return l.writeBytes([]byte(text))
}

func (l *boundedLog) writeBytes(remaining []byte) error {
	for len(remaining) > 0 {
		if l.size >= l.limit {
			if err := l.rotate(); err != nil {
				return err
			}
		}
		amount := min(int64(len(remaining)), l.limit-l.size)
		written, err := l.file.Write(remaining[:amount])
		l.size += int64(written)
		if err != nil {
			return err
		}
		remaining = remaining[written:]
	}
	return nil
}

func (l *boundedLog) rotate() error {
	if err := l.file.Close(); err != nil {
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
	return errors.Join(err, l.file.Close())
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
		return nil, nil, errors.New("application log must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		file.Close()
		return nil, nil, errors.New("application log changed while opening")
	}
	if err := validatePrivateFile(file); err != nil {
		file.Close()
		return nil, nil, err
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
