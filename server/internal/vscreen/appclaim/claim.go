// Package appclaim provides process-scoped locks to the native input host.
// The host retains a Lock until native quiescence or process exit. Daemon callers
// must not acquire it on behalf of a host: their lifetime is not the input lifetime.
package appclaim

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"path/filepath"
	"sync"
)

// Key excludes runtime/profile/backend so all profiles contend for one process incarnation.
type Key struct {
	UID                  uint32
	PID                  int
	ProcessStartIdentity string
}

// Lock holds an open host-owned file descriptor; lockfiles are never unlinked.
type Lock struct {
	mu   sync.Mutex
	file *os.File
}

// Acquire takes a nonblocking lock in a shared, UID-private directory. The native
// host must independently obtain PID/start identity from the OS before this call.
func Acquire(directory string, key Key) (*Lock, error) {
	if key.UID != uint32(os.Getuid()) || key.PID <= 0 || key.ProcessStartIdentity == "" {
		return nil, fmt.Errorf("appclaim: invalid process identity")
	}
	return acquireIdentity(directory, fmt.Appendf(nil, "app:%d:%d:%s", key.UID, key.PID, key.ProcessStartIdentity))
}

// AcquireRuntime is held by the native display host until actual Dispose or host
// exit. Every profile must use the same UID-private directory.
func AcquireRuntime(directory string, key protocol.ResourceKey) (*Lock, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if key.UID != uint32(os.Getuid()) {
		return nil, fmt.Errorf("appclaim: runtime login mismatch")
	}
	raw, err := json.Marshal(key)
	if err != nil {
		return nil, err
	}
	return acquireIdentity(directory, append([]byte("runtime:"), raw...))
}

func acquireIdentity(directory string, identity []byte) (*Lock, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("appclaim directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("appclaim: directory must be private")
	}
	name := fmt.Sprintf("%x.lock", sha256.Sum256(identity))
	file, err := openLocked(filepath.Join(directory, name))
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

// ReleaseAfterQuiescence closes the descriptor only after the host's native barrier.
func (l *Lock) ReleaseAfterQuiescence() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
