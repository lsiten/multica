package jevmodels

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(t.Context(), Config{RootDir: t.TempDir(), PythonPath: "missing-python-executable", IdleTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestSelectionDoesNotDownload(t *testing.T) {
	m := newTestManager(t)
	if len(Catalog()) != 1 || Catalog()[0].Revision == "main" {
		t.Fatal("curated revision missing")
	}
	if _, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("got %v", err)
	}
	entries, err := os.ReadDir(m.cfg.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "manager.lock" {
		t.Fatalf("selection wrote model files: %v", entries)
	}
	if _, err := m.Acquire(t.Context(), Selection{ModelID: "other/repo", Device: "cpu"}); !errors.Is(err, ErrUnknownModel) {
		t.Fatal(err)
	}
}

func TestProfileHasSingleOwner(t *testing.T) {
	m := newTestManager(t)
	if _, err := New(t.Context(), m.cfg); !errors.Is(err, ErrBusy) {
		t.Fatalf("second profile owner allowed: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := New(t.Context(), m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
}

func fakeReady(m *Manager) {
	ctx, cancel := context.WithCancel(m.ctx)
	p := &modelProcess{endpoint: "http://127.0.0.1:1234", token: "secret", device: "cpu", requestedDevice: "cpu", cancel: cancel, done: make(chan struct{})}
	go func() { <-ctx.Done(); close(p.done) }()
	m.installed = true
	m.proc = p
	m.status.State = "ready"
}

func TestLeasesShareProcessAndPreventStop(t *testing.T) {
	m := newTestManager(t)
	fakeReady(m)
	var wg sync.WaitGroup
	leases := make(chan *Lease, 20)
	for range 20 {
		wg.Go(func() {
			l, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"})
			if err != nil {
				t.Error(err)
				return
			}
			leases <- l
		})
	}
	wg.Wait()
	close(leases)
	status, _ := m.Status(ModelID)
	if status.ActiveLeases != 20 {
		t.Fatal(status)
	}
	if err := m.Stop(ModelID); !errors.Is(err, ErrBusy) {
		t.Fatalf("active process stopped: %v", err)
	}
	if err := m.Remove(ModelID); !errors.Is(err, ErrBusy) {
		t.Fatalf("active model removed: %v", err)
	}
	if _, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "mps"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("device switched while busy: %v", err)
	}
	for l := range leases {
		if l.process != m.proc {
			t.Fatal("separate process")
		}
		l.Release()
		l.Release()
	}
	deadline := time.After(time.Second)
	for {
		status, _ = m.Status(ModelID)
		if status.State == "stopped" {
			break
		}
		select {
		case <-deadline:
			t.Fatal(status)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestInstallFailureVisibleAndRetryable(t *testing.T) {
	m := newTestManager(t)
	for range 2 {
		if err := m.Install(t.Context(), ModelID); err == nil {
			t.Fatal("missing interpreter succeeded")
		}
		status, _ := m.Status(ModelID)
		if status.State != "failed" || status.Error == "" {
			t.Fatal(status)
		}
		if _, err := os.Stat(filepath.Join(m.modelDir(), "installed.json")); !os.IsNotExist(err) {
			t.Fatal("partial install accepted")
		}
	}
}

func TestCancelInstallAndClose(t *testing.T) {
	m := newTestManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.installCancel = cancel
	if err := m.CancelInstall(ModelID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("install not cancelled")
	}
	m.installCancel = nil
	fakeReady(m)
	l, err := m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	l.Release()
	if _, err = m.Acquire(t.Context(), Selection{ModelID: ModelID, Device: "cpu"}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestCancelThenImmediateRetryUsesNewGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test helper")
	}
	root := t.TempDir()
	script := filepath.Join(root, "fake-python")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.Context(), Config{RootDir: root, PythonPath: script})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	first, err := m.StartInstall(t.Context(), ModelID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := m.Status(ModelID)
		if status.State == "downloading" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("install did not start: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.CancelInstall(ModelID); err != nil {
		t.Fatal(err)
	}
	second, err := m.StartInstall(t.Context(), ModelID)
	if err != nil {
		t.Fatalf("immediate retry rejected: %v", err)
	}
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("old generation result = %v", err)
	}
	if err := <-second; err == nil {
		t.Fatal("fake installer unexpectedly succeeded")
	}
	status, _ := m.Status(ModelID)
	if status.State != "failed" || status.Error == "" {
		t.Fatalf("new generation status lost: %+v", status)
	}
}

func TestCloseAfterCancelWaitsStaleWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test helper")
	}
	root := t.TempDir()
	script := filepath.Join(root, "fake-python")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 1\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.Context(), Config{RootDir: root, PythonPath: script})
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.StartInstall(t.Context(), ModelID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := m.Status(ModelID)
		if status.State == "downloading" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("install did not start: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.CancelInstall(ModelID); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wait for cancelled worker")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel result = %v", err)
	}
}
