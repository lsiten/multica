package agentdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	Version          = "1.3.1"
	Tag              = "v" + Version
	ExecutableEnv    = "MULTICA_AGENT_DESKTOP_BIN"
	StateRootEnv     = "AGENT_DESKTOP_STATE_ROOT"
	defaultCommand   = "agent-desktop"
	probeTimeout     = 15 * time.Second
	minimumNodeMajor = 18
)

var (
	ErrUnavailable = errors.New("agent-desktop is unavailable")
	ErrVersion     = errors.New("agent-desktop version mismatch")
)

type UnavailableError struct {
	Path   string
	Reason string
}

func (e *UnavailableError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s", ErrUnavailable, e.Reason)
	}
	return fmt.Sprintf("%s at %s: %s", ErrUnavailable, e.Path, e.Reason)
}

func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

type VersionError struct {
	Path string
	Got  string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("%s at %s: got %q, want %q", ErrVersion, e.Path, e.Got, Version)
}

func (e *VersionError) Unwrap() error { return ErrVersion }

type versionEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Version string `json:"version"`
		OS      string `json:"os"`
		Target  string `json:"target"`
	} `json:"data"`
	Error json.RawMessage `json:"error"`
}

// ManagedExecutablePath returns the platform-specific pinned binary path.
func ManagedExecutablePath(home string) string {
	name := defaultCommand
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".multica", "tools", "agent-desktop", Version, name)
}

// Resolve returns the exact pinned binary. The environment override exists
// for development and managed deployments; it is never resolved through PATH.
func Resolve() (string, error) {
	if override := strings.TrimSpace(os.Getenv(ExecutableEnv)); override != "" {
		if !filepath.IsAbs(override) {
			return "", &UnavailableError{Path: override, Reason: "override must be an absolute path"}
		}
		return requireExecutable(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", &UnavailableError{Reason: fmt.Sprintf("resolve home: %v", err)}
	}
	return requireExecutable(ManagedExecutablePath(home))
}

func requireExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &UnavailableError{Path: path, Reason: fmt.Sprintf("install pinned release %s", Tag)}
		}
		return "", &UnavailableError{Path: path, Reason: err.Error()}
	}
	if !info.Mode().IsRegular() {
		return "", &UnavailableError{Path: path, Reason: "not a regular file"}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", &UnavailableError{Path: path, Reason: "not executable"}
	}
	return path, nil
}

// Verify checks the exact release before any desktop session or tool call.
func Verify(ctx context.Context, path string) error {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	command := exec.CommandContext(probeCtx, path, "version")
	command.Env = bridgeEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return &UnavailableError{Path: path, Reason: executableFailure(err)}
	}
	var envelope versionEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil || !envelope.OK {
		return &UnavailableError{Path: path, Reason: "invalid version envelope"}
	}
	if envelope.Data.Version != Version {
		return &VersionError{Path: path, Got: envelope.Data.Version}
	}
	return nil
}

// PlatformHelpers lists the additional release executables required beside the
// main binary on the current platform.
func PlatformHelpers() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"agent-desktop-macos-helper"}
	case "linux":
		return []string{"agent-desktop-linux-clipboard-owner"}
	default:
		return nil
	}
}

func bridgeEnvironment(base []string) []string {
	result := make([]string, 0, len(base)+1)
	found := false
	for _, value := range base {
		if strings.HasPrefix(value, StateRootEnv+"=") {
			found = true
		}
		result = append(result, value)
	}
	if !found {
		home, err := os.UserHomeDir()
		if err == nil {
			result = append(result, StateRootEnv+"="+filepath.Join(home, ".agent-desktop"))
		}
	}
	return result
}

func executableFailure(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return message
		}
	}
	return err.Error()
}
