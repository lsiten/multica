//go:build darwin

package nativecapture

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

	"golang.org/x/sys/unix"
)

func Start(ctx context.Context, config Config) (*Client, error) {
	if !filepath.IsAbs(config.Executable) || config.Build == "" ||
		len(config.Build) > 512 || strings.ContainsAny(config.Build, "\x00\r\n") {
		return nil, ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stat, err := os.Stat(config.Executable)
	if err != nil {
		return nil, fmt.Errorf("native capture executable: %w", err)
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0o111 == 0 {
		return nil, ErrProtocol
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
	parent := os.NewFile(uintptr(fds[0]), "capture-control-parent")
	child := os.NewFile(uintptr(fds[1]), "capture-control-child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, err
	}
	bootstrap, writer, err := os.Pipe()
	if err != nil {
		conn.Close()
		child.Close()
		return nil, err
	}
	defer bootstrap.Close()
	defer writer.Close()
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		conn.Close()
		child.Close()
		return nil, err
	}
	defer clear(token[:])
	media, mediaChild, err := mediaSocketPair()
	if err != nil {
		conn.Close()
		child.Close()
		return nil, err
	}
	defer mediaChild.Close()
	command := exec.Command(config.Executable, "internal-mirror-capture-host")
	command.ExtraFiles = []*os.File{child, bootstrap, mediaChild}
	command.WaitDelay = config.ShutdownTimeout
	command.Stderr, command.Stdout = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		conn.Close()
		child.Close()
		media.Close()
		return nil, fmt.Errorf("start native capture: %w", err)
	}
	child.Close()
	bootstrap.Close()
	client := &Client{
		media: media, streams: make(map[string]*Stream), mediaDone: make(chan struct{}),
		conn: conn, cmd: command, build: config.Build, timeout: config.CallTimeout,
		shutdownTimeout: config.ShutdownTimeout, gate: make(chan struct{}, 1),
		closed: make(chan struct{}), exited: make(chan struct{}),
	}
	client.gate <- struct{}{}
	go client.readMedia()
	go func() {
		_ = command.Wait()
		client.abort()
		close(client.exited)
	}()
	go func() { <-client.closed; _ = client.Close() }()
	deadline, _ := ctx.Deadline()
	if err := writer.SetWriteDeadline(deadline); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	if _, err := writer.Write(token[:]); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	writer.Close()
	if err := media.SetWriteDeadline(deadline); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	if _, err := media.Write(token[:]); err != nil {
		return nil, errors.Join(err, client.Close())
	}
	response, err := client.exchange(ctx, Request{Operation: "hello", Token: token[:], Media: true})
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	client.epoch = response.Epoch.NativeEpoch
	return client, nil
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
	parent := os.NewFile(uintptr(fds[0]), "capture-media-parent")
	child := os.NewFile(uintptr(fds[1]), "capture-media-child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	return conn, child, nil
}
