package runtimeproc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LaunchConfig pins a regular executable by canonical path and SHA256. Environment
// is an explicit allowlist of values, never an inherited process environment.
type LaunchConfig struct {
	Executable     string
	SHA256         string
	Environment    map[string]string
	Bootstrap      Bootstrap
	StartupTimeout time.Duration
}

// Process owns only the child it started. Root cancellation terminates and reaps
// that child; independent domain descendants remain the domain handler's duty.
// No automatic restart or PID-based adoption is attempted.
type Process struct {
	Client      *Client
	cmd         *exec.Cmd
	done        chan struct{}
	watcherDone chan struct{}
	mu          sync.Mutex
	waitErr     error
}

// Start waits for authenticated identity/build/protocol readiness. A startup
// timeout kills and reaps only this launch, never a pre-existing owner.
func Start(ctx context.Context, config LaunchConfig) (*Process, error) {
	return start(ctx, config, []string{Entrypoint})
}
func start(ctx context.Context, config LaunchConfig, args []string) (*Process, error) {
	timeout := config.StartupTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if timeout < 0 || timeout > 10*time.Minute {
		return nil, errors.New("startup timeout must be positive and no greater than ten minutes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Bootstrap.validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(config.Executable) {
		return nil, errors.New("runtime executable must be pinned by absolute path")
	}
	canonical, err := filepath.EvalSymlinks(config.Executable)
	if err != nil {
		return nil, err
	}
	if canonical != config.Executable {
		return nil, errors.New("runtime executable must not contain symlinks")
	}
	executable, err := os.Open(config.Executable)
	if err != nil {
		return nil, err
	}
	info, err := executable.Stat()
	if err != nil || !info.Mode().IsRegular() {
		executable.Close()
		return nil, errors.New("runtime executable must be a regular file")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, executable)
	closeErr := executable.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(config.SHA256) != 64 || hex.EncodeToString(hash.Sum(nil)) != config.SHA256 {
		return nil, errors.New("runtime executable digest mismatch")
	}
	dir, err := prepareDirectory(config.Bootstrap.Root, config.Bootstrap.Identity.Scope)
	if err != nil {
		return nil, err
	}
	launchLock, err := lockFile(filepath.Join(dir, "launch.lock"))
	if err != nil {
		return nil, errors.New("another launch is in progress")
	}
	defer launchLock.Close()
	ownerLock, err := lockFile(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return nil, errors.New("runtime owner is live or unknown")
	}
	ownerLock.Close()
	// NewService independently enforces this after acquiring its own ownership lock.
	if raw, readErr := readPrivate(RecordPath(config.Bootstrap.Root, config.Bootstrap.Identity.Scope)); readErr == nil {
		if !replaceableRecord(raw, config.Bootstrap.Identity) {
			return nil, errors.New("runtime prior owner requires reconciliation")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	environment := make([]string, 0, len(config.Environment))
	for key, value := range config.Environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid explicit child environment")
		}
		environment = append(environment, key+"="+value)
	}
	raw, err := json.Marshal(config.Bootstrap)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBody {
		return nil, errors.New("bootstrap too large")
	}
	// A real pipe prevents the credential from appearing in argv or environment.
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.Command(config.Executable, args...)
	cmd.Env = environment
	cmd.Stdin = reader
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start runtime child: %w", err)
	}
	reader.Close()
	p := &Process{cmd: cmd, done: make(chan struct{}), watcherDone: make(chan struct{})}
	go func() { err := cmd.Wait(); p.mu.Lock(); p.waitErr = err; p.mu.Unlock(); close(p.done) }()
	go func() {
		defer close(p.watcherDone)
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-p.done
		case <-p.done:
		}
	}()
	fail := func(cause error) (*Process, error) {
		_ = cmd.Process.Kill()
		<-p.done
		<-p.watcherDone
		return nil, cause
	}
	startupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := startupCtx.Deadline()
	if err = writer.SetWriteDeadline(deadline); err != nil {
		return fail(err)
	}
	if _, err = writer.Write(raw); err != nil {
		return fail(errors.New("runtime bootstrap pipe failed"))
	}
	writer.Close()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, openErr := Open(startupCtx, config.Bootstrap.Root, config.Bootstrap.Identity)
		if openErr == nil && subtle.ConstantTimeCompare([]byte(client.record.Token), []byte(config.Bootstrap.Token)) != 1 {
			return fail(errors.New("runtime bootstrap credential mismatch"))
		}
		if openErr == nil {
			select {
			case <-p.done:
				return fail(errors.New("runtime exited during readiness"))
			default:
			}
			p.Client = client
			return p, nil
		}
		select {
		case <-p.done:
			return fail(errors.New("runtime exited before readiness"))
		case <-startupCtx.Done():
			return fail(errors.New("runtime readiness timed out or launch canceled"))
		case <-ticker.C:
		}
	}
}

// Wait observes actual child exit, not a health timeout. It also joins supervision.
func (p *Process) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
	}
	<-p.watcherDone
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// Stop requests domain cleanup and durable stopped receipt, then waits for exit.
// Failure leaves ownership suspect. Close is the bounded forced cleanup for callers.
func (p *Process) Stop(ctx context.Context) error {
	status, err := p.Client.Health(ctx)
	if err != nil {
		return err
	}
	req, err := p.Client.Request("stop", status.Fence, nil)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(req.Deadline) {
		req.Deadline = deadline
	}
	out, err := p.Client.Call(ctx, req)
	if err != nil {
		return err
	}
	if out.Receipt == nil || out.Receipt.State != "completed" || out.Receipt.Error != nil || out.Status.State != "stopped" {
		return errors.New("runtime stop remains unconfirmed")
	}
	return p.Wait(ctx)
}

// Close forcibly terminates the exact owned process and joins its goroutines.
// It never claims that domain descendants stopped or removes ownership evidence.
func (p *Process) Close() error {
	select {
	case <-p.done:
	default:
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.Wait(ctx)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

// PID returns the child's operating-system process id. It is stable for the
// child's whole lifetime and lets a supervisor observe an orphaned (parent-exited)
// child without keeping the Process handle alive. After the child is reaped the
// number may be reused.
func (p *Process) PID() int {
	return p.cmd.Process.Pid
}

// ReadBootstrap reads one bounded credential envelope and rejects trailing data.
func ReadBootstrap(input io.Reader, build string) (Bootstrap, error) {
	raw, err := io.ReadAll(io.LimitReader(input, maxBody+1))
	if err != nil {
		return Bootstrap{}, err
	}
	if len(raw) > maxBody {
		return Bootstrap{}, errors.New("bootstrap exceeds limit")
	}
	var b Bootstrap
	if json.Unmarshal(bytes.TrimSpace(raw), &b) != nil {
		return b, errors.New("invalid runtime bootstrap")
	}
	if err = b.validate(); err != nil {
		return b, err
	}
	if b.Identity.Build != build {
		return b, errors.New("runtime bootstrap build mismatch")
	}
	return b, nil
}

// RunProbe runs only transport lifecycle, with no domain capabilities. Application
// entrypoints must select a real domain Config explicitly for any other service.
func RunProbe(ctx context.Context, input io.Reader, build string) error {
	b, err := ReadBootstrap(input, build)
	if err != nil {
		return err
	}
	if b.Identity.Scope.Service != "probe" {
		return errors.New("runtime domain service is not implemented")
	}
	s, err := NewService(Config{Bootstrap: b})
	if err != nil {
		return err
	}
	return s.Serve(ctx)
}
