//go:build darwin || linux

package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStartRejectsPATHLookup(t *testing.T) {
	if _, err := Start(context.Background(), Config{Executable: "multica", Build: "test/commit"}); err == nil {
		t.Fatal("relative executable must be rejected before spawning")
	}
}

func testConfig(t *testing.T, mode string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOSTCLIENT_TEST_MODE", mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("HOSTCLIENT_TEST_CLEANUP", filepath.Join(t.TempDir(), "cleanup"))
	return Config{Executable: executable, Build: "test/commit", StartupTimeout: time.Second, CallTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond}
}

func TestHandshakeRejectsUntrustedResponses(t *testing.T) {
	for _, mode := range []string{"wrong-build", "wrong-version", "wrong-id", "missing-epoch", "reject-token", "startup-exit", "partial", "oversize", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			c, err := Start(t.Context(), testConfig(t, mode))
			if err == nil || c != nil {
				if c != nil {
					c.Close()
				}
				t.Fatal("untrusted handshake accepted")
			}
		})
	}
}

func TestStartupDeadline(t *testing.T) {
	cfg := testConfig(t, "startup-stall")
	cfg.StartupTimeout = 50 * time.Millisecond
	started := time.Now()
	if c, err := Start(t.Context(), cfg); err == nil || c != nil {
		t.Fatal("stalled handshake accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("startup cleanup exceeded bound")
	}
}

func TestProcessHandshakeRPCAndEOFCleanup(t *testing.T) {
	cfg := testConfig(t, "")
	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("HOSTCLIENT_TEST_TRACE", trace)
	c, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.NativeEpoch() != "test-native" {
		t.Fatal("missing host identity")
	}
	for range 3 {
		if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Args      []string
		Operation string
	}
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if len(observed.Args) != 2 || observed.Args[0] != cfg.Executable || observed.Args[1] != "internal-vscreen-host" {
		t.Fatalf("unexpected process argv: %s", raw)
	}
	t.Logf("process pid=%d argv=%s; token verified exclusively from FD4 and FD3", c.cmd.Process.Pid, raw)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.cmd.ProcessState == nil || !c.cmd.ProcessState.Exited() {
		t.Fatal("child not reaped")
	}
	cleanup, err := os.ReadFile(os.Getenv("HOSTCLIENT_TEST_CLEANUP"))
	if err != nil || string(cleanup) != "EOF: owned resources released\n" {
		t.Fatalf("EOF cleanup missing: %s %v", cleanup, err)
	}
	t.Logf("cleanup=%s child reaped=true", cleanup)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed stream reused: %v", err)
	}
}

func TestQueuedCancellationPreservesNextFrame(t *testing.T) {
	c, err := Start(t.Context(), testConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-c.gate
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := c.Call(ctx, native.Request{Operation: "list"}); result <- err }()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.gate <- struct{}{}
	if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); err != nil {
		t.Fatalf("queued cancellation poisoned stream: %v", err)
	}
}

func TestInflightFailureClosesAndReapsWithoutReplay(t *testing.T) {
	for _, mode := range []string{"call-exit", "call-stall", "epoch-drift", "partial-call"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t, mode)
			c, err := Start(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if _, err := c.Call(ctx, native.Request{Operation: "ensure", Resource: protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}, Width: 1600, Height: 900}); err == nil {
				t.Fatal("failed call succeeded")
			}
			if _, err := c.Call(t.Context(), native.Request{Operation: "list"}); !errors.Is(err, ErrClosed) {
				t.Fatalf("uncertain stream reused: %v", err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-c.exited:
			default:
				t.Fatal("helper not reaped")
			}
		})
	}
}

func TestRemoteErrorsAreTypedAndSanitized(t *testing.T) {
	for _, mode := range []string{"remote-error", "hostile-error"} {
		t.Run(mode, func(t *testing.T) {
			c, err := Start(t.Context(), testConfig(t, mode))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_, err = c.Call(t.Context(), native.Request{Operation: "list"})
			var remote *RemoteError
			if !errors.As(err, &remote) {
				t.Fatalf("missing typed remote error: %v", err)
			}
			want := "geometry_conflict"
			if mode == "hostile-error" {
				want = "unavailable"
			}
			if remote.Code != want {
				t.Fatal("host-controlled text escaped sanitization")
			}
		})
	}
}

func TestCloseKillsOnlyOwnedUnresponsiveHelper(t *testing.T) {
	c, err := Start(t.Context(), testConfig(t, "ignore-eof"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if c.cmd.ProcessState == nil {
		t.Fatal("unresponsive owned helper not reaped")
	}
}
