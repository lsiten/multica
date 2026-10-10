//go:build !windows

package jevmodels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestModelEngineCancellationReapsGrandchildHoldingOutput(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "grandchild.pid")
	script := filepath.Join(root, "fixture-python")
	body := "#!/bin/sh\nsleep 60 &\nchild=$!\necho $child > '" + pidPath + "'\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(t.Context(), Config{RootDir: root, PythonPath: script, PersistInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ctx, cancel := context.WithCancel(t.Context())
	result, err := manager.StartInstall(ctx, ModelID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		raw, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		t.Fatal("fixture grandchild did not start")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel result %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("process tree did not stop")
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("grandchild still exists pid=%d: %v", pid, err)
	}
}
