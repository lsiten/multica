//go:build darwin

package native

import (
	"context"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAppPrivateParentAuthentication(t *testing.T) {
	if os.Getenv("MULTICA_TEST_APP_FD_CHILD") == "1" {
		conn, err := openPrivateParent(6, [32]byte{1})
		if err != nil {
			os.Exit(21)
		}
		conn.Close()
		os.Exit(0)
	}
	for _, mode := range []string{"authenticated", "wrong-token", "aliased-media"} {
		t.Run(mode, func(t *testing.T) {
			pair := func() (net.Conn, *os.File) {
				t.Helper()
				fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
				if err != nil {
					t.Fatal(err)
				}
				unix.CloseOnExec(fds[0])
				unix.CloseOnExec(fds[1])
				file := os.NewFile(uintptr(fds[0]), "parent")
				child := os.NewFile(uintptr(fds[1]), "child")
				conn, err := net.FileConn(file)
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close(); child.Close() })
				return conn, child
			}
			_, controlChild := pair()
			_, mediaChild := pair()
			app, appChild := pair()
			bootstrap, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer bootstrap.Close()
			defer writer.Close()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAppPrivateParentAuthentication$")
			cmd.Env = append(os.Environ(), "MULTICA_TEST_APP_FD_CHILD=1", "GORACE=atexit_sleep_ms=0")
			if mode == "aliased-media" {
				appChild = mediaChild
			}
			cmd.ExtraFiles = []*os.File{controlChild, bootstrap, mediaChild, appChild}
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			token := [32]byte{1}
			if mode == "wrong-token" {
				token[0] = 2
			}
			app.SetWriteDeadline(time.Now().Add(time.Second))
			if mode != "aliased-media" {
				if _, err := app.Write(token[:]); err != nil {
					t.Fatal(err)
				}
			}
			err = cmd.Wait()
			if (err == nil) != (mode == "authenticated") {
				t.Fatalf("FD6 authentication mode=%s err=%v", mode, err)
			}
		})
	}
}
