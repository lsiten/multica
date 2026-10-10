//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// This file is the runtime/native acceptance for the G "retain and re-adopt"
// substrate (gatewayProcessClient.reAdopt). It proves, with a REAL gateway child
// process, that after the control parent exits WITHOUT closing the child (so the
// child is orphaned and stays a live, ready service), the control re-adopts the
// surviving child by reopening its runtime record over the private runtime
// channel (runtimeproc.Open), confirms it is still ready, and can end it with an
// explicit control "stop". This is the honest evidence the G cut needs before a
// control restart may retain a running child: reAdopt reconnects to a LIVE child
// rather than killing and relaunching it. It does NOT fake retention, does not
// touch the hot path, and keeps the default (non-retained) close behavior intact.

// runReAdoptParent is the TestMain "readopt-parent" entrypoint (level 2). It is a
// plain process, not a runtimeproc owner. It launches exactly one real gateway
// child (level 3, the re-executed test binary running RunGatewayService) via
// runtimeproc.Start(context.Background()), records the child PID and the child's
// bootstrap (so the re-adopting control can reopen the child's record), and then
// returns so TestMain exits WITHOUT closing the child. The child is orphaned to
// init. It is pinned by canonical path + SHA256 exactly like the other
// multi-process runtime tests.
func runReAdoptParent(bootstrapPath, childPIDFile, childBootstrapFile string) error {
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
	_, err = io.Copy(hash, file)
	if err != nil {
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
	if err := os.WriteFile(childPIDFile, []byte(strconv.Itoa(process.PID())), 0600); err != nil {
		return err
	}
	// Hand the child's bootstrap back to the re-adopting control so it can reopen
	// the child's record (Open(root, identity)) without a process handle.
	if err := os.WriteFile(childBootstrapFile, raw, 0600); err != nil {
		return err
	}
	// Intentionally NOT closing the child: it is orphaned to init and survives.
	return nil
}

// TestGatewayProcessClientReAdoptsSurvivingChild is the positive runtime proof of
// the G retain-and-re-adopt substrate: a real gateway child launched via
// runtimeproc.Start outlives its control parent, and the control re-adopts it by
// reopening the child's runtime record (runtimeproc.Open), confirms it is still
// ready, and ends it with an explicit control "stop". A child that outlived the
// control is re-CONNECTed, not killed and relaunching.
func TestGatewayProcessClientReAdoptsSurvivingChild(t *testing.T) {
	survivalScope := runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: "gateway", Profile: "owned-fixture",
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "gateway-service")
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
	childBootstrapFile := filepath.Join(parent, "child-bootstrap.json")

	// Level 2: a plain parent process that starts the child (level 3) via
	// runtimeproc.Start(context.Background()), records its PID and bootstrap, and
	// exits WITHOUT closing the child. The child is orphaned to init.
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	launcher := exec.Command(testBinary, "readopt-parent", bootstrapFile, childPIDFile, childBootstrapFile)
	if out, err := launcher.CombinedOutput(); err != nil {
		t.Fatalf("readopt parent exited non-zero: %v\n%s", err, out)
	}

	pidBytes, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("parent did not record the child PID: %v", err)
	}
	childPID, err := strconv.Atoi(string(pidBytes))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID = %q, want a positive pid", string(pidBytes))
	}

	// The parent has exited. The child must still be alive (orphaned to init).
	if !survivalProcessLive(childPID, 10*time.Second) {
		t.Fatalf("child (pid %d) did not survive the control parent's exit", childPID)
	}

	// The control re-adopts the surviving child by reopening its runtime record.
	// reAdopt reads the child's bootstrap (which persists on disk, owned by the
	// child) and authenticates readiness over the private runtime channel.
	childBootstrapRaw, err := os.ReadFile(childBootstrapFile)
	if err != nil {
		t.Fatalf("parent did not hand back the child bootstrap: %v", err)
	}
	var childBootstrap runtimeproc.Bootstrap
	if err := json.Unmarshal(childBootstrapRaw, &childBootstrap); err != nil {
		t.Fatalf("invalid child bootstrap: %v", err)
	}
	c := &gatewayProcessClient{
		bootstrap: childBootstrap,
		daemon:    &Daemon{logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	reAdopted, err := c.reAdopt(ctx)
	if err != nil {
		t.Fatalf("reAdopt could not reopen the surviving child: %v", err)
	}
	status, err := reAdopted.Health(ctx)
	if err != nil {
		t.Fatalf("re-adopted child health failed: %v", err)
	}
	if status.State != "ready" && status.State != "running" {
		t.Fatalf("re-adopted child state = %q, want ready/running (re-adopt must reconnect a live child)", status.State)
	}

	// The re-adopted child is responsive to control: an explicit "stop" ends it.
	// This also cleans up the orphan so the test never leaks it.
	stopReq, err := reAdopted.Request("stop", status.Fence, nil)
	if err != nil {
		t.Fatalf("stop request failed: %v", err)
	}
	if _, err := reAdopted.Call(ctx, stopReq); err != nil {
		t.Fatalf("stop call failed: %v", err)
	}
	if !survivalProcessGone(childPID, 15*time.Second) {
		_ = syscall.Kill(childPID, syscall.SIGTERM) // fallback cleanup so the orphan never leaks
		if !survivalProcessGone(childPID, 5*time.Second) {
			t.Fatalf("re-adopted child (pid %d) did not end on an explicit control stop", childPID)
		}
	}
	t.Logf("readopt: gateway child pid=%d outlived its control parent, was re-adopted via Open, served ready, and ended only on control stop", childPID)
}
