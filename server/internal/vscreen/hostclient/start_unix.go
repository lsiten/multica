//go:build darwin || linux

package hostclient

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"golang.org/x/sys/unix"
)

// Start authenticates the helper before exposing it. Failure closes all inherited
// descriptors and reaps the child; no capability is passed in argv or environment.
func Start(ctx context.Context, config Config) (*Client, error) {
	if !filepath.IsAbs(config.Executable) || config.Build == "" || len(config.Build) > 512 || strings.ContainsAny(config.Build, "\x00\r\n") {
		return nil, native.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stat, err := os.Stat(config.Executable)
	if err != nil {
		return nil, fmt.Errorf("native executable: %w", err)
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0111 == 0 {
		return nil, native.ErrProtocol
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 5 * time.Second
	}
	if config.CallTimeout <= 0 {
		config.CallTimeout = 3 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, config.StartupTimeout)
	defer cancel()
	// ForkLock closes the socketpair-to-CLOEXEC race with other concurrent execs.
	syscall.ForkLock.Lock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	syscall.ForkLock.Unlock()
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "vscreen-control-parent")
	child := os.NewFile(uintptr(fds[1]), "vscreen-control-child")
	defer parent.Close()
	defer child.Close()
	conn, err := net.FileConn(parent)
	if err != nil {
		return nil, err
	}
	parent.Close()
	bootstrap, writer, err := os.Pipe()
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer bootstrap.Close()
	defer writer.Close()
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		conn.Close()
		return nil, err
	}
	defer clear(token[:])
	cmd := exec.Command(config.Executable, "internal-vscreen-host")
	cmd.ExtraFiles = []*os.File{child, bootstrap}
	var media net.Conn
	if config.Media {
		var mediaChild *os.File
		media, mediaChild, err = mediaSocketPair()
		if err != nil {
			conn.Close()
			return nil, err
		}
		defer mediaChild.Close()
		cmd.ExtraFiles = append(cmd.ExtraFiles, mediaChild)
	}
	cmd.WaitDelay = config.ShutdownTimeout
	// Discard child diagnostics: hostile/native stderr never becomes a secret log
	// or an unbounded buffer, and stdout cannot corrupt the control stream.
	cmd.Stderr, cmd.Stdout = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		if media != nil {
			media.Close()
		}
		conn.Close()
		return nil, fmt.Errorf("start native host: %w", err)
	}
	child.Close()
	bootstrap.Close()
	c := &Client{media: media, streams: make(map[string]*Stream), mediaDone: make(chan struct{}), conn: conn, cmd: cmd, build: config.Build, timeout: config.CallTimeout, shutdownTimeout: config.ShutdownTimeout, gate: make(chan struct{}, 1), closed: make(chan struct{}), exited: make(chan struct{})}
	c.gate <- struct{}{}
	if media != nil {
		go c.readMedia()
	} else {
		close(c.mediaDone)
	}
	go func() {
		// Exit status is deliberately not surfaced with child-controlled stderr.
		_ = cmd.Wait()
		c.abort()
		close(c.exited)
	}()
	// Automatic teardown also covers callers that abandon an uncertain operation.
	go func() { <-c.closed; _ = c.Close() }()
	if _, err := writer.Write(token[:]); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	writer.Close()
	if media != nil {
		deadline, _ := ctx.Deadline()
		if err := media.SetWriteDeadline(deadline); err != nil {
			return nil, errors.Join(err, c.Close())
		}
		if _, err := media.Write(token[:]); err != nil {
			return nil, errors.Join(err, c.Close())
		}
	}
	response, err := c.exchange(ctx, native.Request{Operation: "hello", Token: token[:], Media: config.Media})
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	c.epoch = response.Epoch.NativeEpoch
	return c, nil
}

func mediaSocketPair() (net.Conn, *os.File, error) {
	syscall.ForkLock.Lock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	syscall.ForkLock.Unlock()
	if err != nil {
		return nil, nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "vscreen-media-parent")
	child := os.NewFile(uintptr(fds[1]), "vscreen-media-child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	return conn, child, nil
}
