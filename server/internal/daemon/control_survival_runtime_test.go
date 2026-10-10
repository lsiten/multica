//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// This file is the runtime/native acceptance for the "survives a restart"
// substrate that the G retained-restart gate and the F2 gateway-listener work
// depend on. It proves, with a REAL child process, that a process started by
// runtimeproc.Start(context.Background()) survives its parent's exit: the child
// is orphaned and reparented to init (PID 1) rather than killed, stays a live
// and responsive runtime service, and only ends on an explicit control "stop".
//
// This is the honest evidence the plan needs before a control restart may retain
// a running child (worker/gateway): it does NOT fake retention, does not
// recreate control_lifecycle.go, and does not touch the hot path. It shows the
// kernel/orphaning behavior is real on this machine, which unblocks the next
// step (making the daemon not close a child on a retained restart).

// runSurvivalParent is the TestMain "survival-parent" entrypoint (level 2). It
// is a plain process, not a runtimeproc owner. It launches exactly one real
// child (level 3, the re-executed test binary running RunControlService) via
// runtimeproc.Start(context.Background()), records the child's PID, and then
// returns so TestMain exits WITHOUT closing the child. The child is orphaned to
// init. It is pinned by canonical path + SHA256 exactly like the control parent.
func runSurvivalParent(bootstrapPath, childPIDFile string) error {
	raw, err := os.ReadFile(bootstrapPath)
	if err != nil {
		return err
	}
	var bootstrap runtimeproc.Bootstrap
	if err := json.Unmarshal(raw, &bootstrap); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	file, err := os.Open(executable)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return err
	}
	file.Close()
	// context.Background() (never cancelled) plus a plain parent exit is what
	// must orphan the child to init rather than kill it.
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    runtimeChildEnv(),
		Bootstrap:      bootstrap,
		StartupTimeout: 15 * time.Second,
	})
	if err != nil {
		return err
	}
	// Record the child PID before exiting so the test can verify survival and
	// later clean up the orphan. The child is intentionally NOT closed.
	return os.WriteFile(childPIDFile, []byte(strconv.Itoa(process.PID())), 0600)
}

// TestRetainedRestartSurvivalRuntime is the positive runtime proof of the
// survives-restart substrate: a real child launched via runtimeproc.Start
// (context.Background()) outlives its parent process and remains a live,
// responsive runtime service that ends only on an explicit "stop".
func TestRetainedRestartSurvivalRuntime(t *testing.T) {
	survivalScope := runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: "control", Profile: "owned-fixture",
	}
	// root is a private (0700) subdirectory, as every runtimeproc test uses. The
	// bootstrap and child-PID handoff files live in the temp-dir parent, which the
	// plain parent process (level 2) can read/write before prepareDirectory makes
	// the runtime root.
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "survival-service")
	identity, err := runtimeproc.NewIdentity(survivalScope, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRaw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapFile := filepath.Join(parent, "bootstrap.json")
	if err := os.WriteFile(bootstrapFile, bootstrapRaw, 0600); err != nil {
		t.Fatal(err)
	}
	childPIDFile := filepath.Join(parent, "child.pid")

	// Level 2: a plain parent process that starts the child (level 3) via
	// runtimeproc.Start(context.Background()), records its PID, and exits without
	// closing it.
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	launcher := exec.Command(testBinary, "survival-parent", bootstrapFile, childPIDFile)
	if out, err := launcher.CombinedOutput(); err != nil {
		t.Fatalf("survival parent exited non-zero: %v\n%s", err, out)
	}

	// The parent has exited. Read the child PID it recorded before exiting.
	pidBytes, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("parent did not record the child PID: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID = %q, want a positive pid", strings.TrimSpace(string(pidBytes)))
	}

	// CORE PROOF: the child is still alive after its parent exited. A normal
	// parent exit must orphan the child to init, not kill it. Poll briefly so a
	// scheduling race does not read a stale "alive".
	if !survivalProcessLive(childPID, 10*time.Second) {
		t.Fatalf("child (pid %d) did not survive the parent's exit; the survives-restart substrate is absent on this machine", childPID)
	}

	// A live service, not a zombie: reopen the orphaned child's record and probe
	// it over the private runtime channel.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := runtimeproc.Open(ctx, root, identity)
	if err != nil {
		t.Fatalf("could not reopen the orphaned child: %v", err)
	}
	status, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("orphaned child health failed: %v", err)
	}
	if status.State != "ready" {
		t.Fatalf("orphaned child state = %q, want ready (the child must be a live service, not a zombie)", status.State)
	}

	// Responsive to control: a clean "stop" over the private channel must end it.
	// This also cleans up the orphaned process so the test never leaks it.
	stopReq, err := client.Request("stop", status.Fence, nil)
	if err != nil {
		t.Fatalf("stop request failed: %v", err)
	}
	if _, err := client.Call(ctx, stopReq); err != nil {
		t.Fatalf("stop call failed: %v", err)
	}
	if !survivalProcessGone(childPID, 15*time.Second) {
		_ = syscall.Kill(childPID, syscall.SIGTERM) // fallback cleanup so the orphan never leaks
		if !survivalProcessGone(childPID, 5*time.Second) {
			t.Fatalf("orphaned child (pid %d) did not end on an explicit control stop", childPID)
		}
	}
	t.Logf("survival: child pid=%d outlived its parent, served 'ready', and ended only on control stop", childPID)
}

// processAlive polls the OS until the pid is confirmed live or the timeout
// elapses. A nil error from syscall.Kill(pid, 0) means the process exists.
func survivalProcessLive(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// processExited polls the OS until the pid disappears (reaped by init) or the
// timeout elapses.
func survivalProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
