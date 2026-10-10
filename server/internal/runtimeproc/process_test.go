package runtimeproc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeChild(t *testing.T) {
	mode := os.Getenv("RUNTIMEPROC_FIXTURE")
	if mode == "" {
		return
	}
	if os.Getenv("MULTICA_TOKEN") != "" {
		os.Exit(90)
	}
	b, err := ReadBootstrap(os.Stdin, "fixture")
	if err != nil {
		os.Exit(91)
	}
	if mode == "pid-only" {
		<-time.NewTimer(time.Hour).C
	}
	if mode == "wrong-token" {
		b.Token = strings.Repeat("0", 64)
	}
	if mode == "wrong-build" {
		b.Identity.Build = "wrong"
	}
	s, err := NewService(Config{Bootstrap: b, Capabilities: []string{"increment"}, ReadCapabilities: []string{"catalog"}, ReadHandler: func(context.Context, Request) (json.RawMessage, *Error) { return json.RawMessage(`{"models":[]}`), nil }, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
		if string(r.Payload) == `{"wait":true}` {
			<-ctx.Done()
		}
		return json.RawMessage(`{"applied":true}`), nil
	}})
	if err != nil {
		os.Exit(92)
	}
	if err = s.Serve(context.Background()); err != nil {
		os.Exit(93)
	}
	os.Exit(0)
}
func launchFixture(t *testing.T, mode string) LaunchConfig {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return LaunchConfig{Executable: path, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: map[string]string{"RUNTIMEPROC_FIXTURE": mode}, Bootstrap: testBootstrap(t), StartupTimeout: 2 * time.Second}
}
func startFixture(t *testing.T, ctx context.Context, cfg LaunchConfig) *Process {
	t.Helper()
	p, err := start(ctx, cfg, []string{"-test.run=^TestRuntimeChild$"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}
func TestProcessReadinessCancellationAndDuplicateLaunch(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "must-not-be-inherited")
	cfg := launchFixture(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := startFixture(t, ctx, cfg)
	if _, err := start(context.Background(), cfg, []string{"-test.run=^TestRuntimeChild$"}); err == nil {
		t.Fatal("duplicate child accepted")
	}
	status, err := p.Client.Handshake(context.Background())
	if err != nil || status.State != "ready" {
		t.Fatal("child not ready")
	}
	cancel()
	waitCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	var exit *exec.ExitError
	if err = p.Wait(waitCtx); !errors.As(err, &exit) {
		t.Fatalf("root cancellation did not terminate child: %v", err)
	}
	if _, err = start(context.Background(), cfg, []string{"-test.run=^TestRuntimeChild$"}); err == nil {
		t.Fatal("crashed owner silently replaced")
	}
}
func TestProcessRejectsMisleadingReadinessAndPinnedExecutable(t *testing.T) {
	for _, mode := range []string{"pid-only", "wrong-build", "wrong-token"} {
		t.Run(mode, func(t *testing.T) {
			cfg := launchFixture(t, mode)
			cfg.StartupTimeout = 100 * time.Millisecond
			if _, err := start(context.Background(), cfg, []string{"-test.run=^TestRuntimeChild$"}); err == nil {
				t.Fatal("unverified child accepted")
			}
			// The owner lock must be released by the reaped process, even though records remain uncertain.
			lock, err := lockFile(filepath.Join(scopeDirectory(cfg.Bootstrap.Root, cfg.Bootstrap.Identity.Scope), "owner.lock"))
			if err != nil {
				t.Fatalf("failed launch was not reaped: %v", err)
			}
			lock.Close()
		})
	}
	cfg := launchFixture(t, "normal")
	cfg.SHA256 = strings.Repeat("0", 64)
	if _, err := Start(context.Background(), cfg); err == nil {
		t.Fatal("wrong executable digest accepted")
	}
	cfg = launchFixture(t, "normal")
	cfg.Executable = "multica"
	if _, err := Start(context.Background(), cfg); err == nil {
		t.Fatal("PATH lookup accepted")
	}
}
func TestReadOnlyQuotaAcknowledgementAndRetiredReplay(t *testing.T) {
	b := testBootstrap(t)
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"change"}, ReadCapabilities: []string{"catalog"}, ReadHandler: func(context.Context, Request) (json.RawMessage, *Error) { return json.RawMessage(`{"models":[]}`), nil }, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return json.RawMessage(`{}`), nil }})
	for range 150 {
		if _, err := c.Read(context.Background(), "catalog", nil); err != nil {
			t.Fatal(err)
		}
	}
	before, err := ReadRecord(b.Root, b.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Operations) != 0 || before.Fence.Revision != 1 {
		t.Fatal("readonly call consumed mutation state")
	}
	first := request(t, c, "change", "{}")
	call(t, c, first)
	for range maxOperations - 1 {
		call(t, c, request(t, c, "change", "{}"))
	}
	wantCode(t, c, request(t, c, "change", "{}"), "resource_exhausted")
	drained := call(t, c, request(t, c, "drain", ""))
	if drained.Status.State != "draining" {
		t.Fatal("quota blocked drain")
	}
	ack := request(t, c, "acknowledge", "")
	out := call(t, c, ack)
	if out.Status.Fence.ResourceEpoch != 1 || out.Status.Fence.SupervisorEpoch != 1 || out.Status.ReplayEpoch != 2 {
		t.Fatal("receipt retirement changed domain epoch")
	}
	call(t, c, ack)
	_, err = c.QueryOperation(context.Background(), first.RequestID)
	var problem *Error
	if !errors.As(err, &problem) || problem.Code != "retired_request" {
		t.Fatalf("retired query error %v", err)
	}
	wantCode(t, c, first, "retired_request")
	first.Fence = out.Status.Fence
	first.ReplayEpoch = 2
	wantCode(t, c, first, "retired_request")
	call(t, c, request(t, c, "resume", ""))
	call(t, c, request(t, c, "change", "{}"))
	call(t, c, request(t, c, "stop", ""))
}
func TestCurlProcessSurface(t *testing.T) {
	artifact := os.Getenv("RUNTIMEPROC_QA_ARTIFACT")
	if artifact == "" {
		t.Skip("explicit artifact path required for curl surface QA")
	}
	cfg := launchFixture(t, "normal")
	p := startFixture(t, context.Background(), cfg)
	var transcript strings.Builder
	curl := func(req Request, authenticated bool, want int) Response {
		t.Helper()
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var config strings.Builder
		if authenticated {
			fmt.Fprintf(&config, "header = %q\n", "Authorization: Bearer "+p.Client.record.Token)
		}
		fmt.Fprintf(&config, "header = %q\ndata = %q\n", "Content-Type: application/json", string(raw))
		// Credential is private stdin; the literal curl -i command and response are safe evidence.
		command := exec.Command("curl", "-i", "--silent", "--show-error", "--max-time", "3", "--config", "-", p.Client.record.Address+"/rpc")
		command.Stdin = strings.NewReader(config.String())
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("curl: %v", err)
		}
		fmt.Fprintf(&transcript, "$ curl -i --silent --show-error --max-time 3 --config - <private-fixture-config> [loopback]/rpc\nscenario=%s authenticated=%t\n%s\n", req.Operation, authenticated, output)
		if !strings.Contains(string(output), fmt.Sprintf("HTTP/1.1 %d", want)) {
			t.Fatalf("status want=%d response=%s", want, output)
		}
		parts := strings.SplitN(string(output), "\r\n\r\n", 2)
		if len(parts) != 2 {
			t.Fatal("invalid curl HTTP response")
		}
		var out Response
		if err = json.Unmarshal([]byte(parts[1]), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	r := request(t, p.Client, "handshake", "")
	curl(r, true, 200)
	curl(r, false, 401)
	mutation := request(t, p.Client, "increment", `{"value":1}`)
	out := curl(mutation, true, 200)
	if out.Receipt.State != "completed" {
		t.Fatal("mutation not complete")
	}
	changed := mutation
	changed.Payload = json.RawMessage(`{"value":2}`)
	curl(changed, true, 409)
	expired := request(t, p.Client, "increment", "{}")
	expired.Deadline = time.Now().Add(-time.Second)
	curl(expired, true, 408)
	if out = curl(request(t, p.Client, "drain", ""), true, 200); out.Status.State != "draining" {
		t.Fatal("drain not observable")
	}
	if out = curl(request(t, p.Client, "stop", ""), true, 200); out.Status.State != "stopped" {
		t.Fatal("stop not observable")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&transcript, "cleanup: child pid=%d Wait returned nil; process exited and supervision joined\n", p.cmd.Process.Pid)
	if strings.Contains(transcript.String(), cfg.Bootstrap.Token) {
		t.Fatal("credential leaked into artifact")
	}
	if err := os.WriteFile(artifact, []byte(transcript.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedMutationReceiptSurvivesChildCrash(t *testing.T) {
	cfg := launchFixture(t, "normal")
	p := startFixture(t, context.Background(), cfg)
	r := request(t, p.Client, "increment", `{"wait":true}`)
	done := make(chan error, 1)
	go func() { _, err := p.Client.Call(context.Background(), r); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		record, err := ReadRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
		if err == nil && record.Operations[r.RequestID].State == "pending" {
			found = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !found {
		t.Fatal("intent was not persisted before executing domain handler")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("crashed child returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client remained blocked after crash")
	}
	record, err := ReadRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
	if err != nil || record.Operations[r.RequestID].State != "pending" {
		t.Fatal("crash lost uncertain receipt")
	}
	if _, err = start(context.Background(), cfg, []string{"-test.run=^TestRuntimeChild$"}); err == nil {
		t.Fatal("interrupted operation automatically restarted")
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == Entrypoint {
		if err := RunProbe(context.Background(), os.Stdin, "fixture"); err != nil {
			os.Exit(94)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestProductionLaunchEntrypointAndDomainRejection(t *testing.T) {
	cfg := launchFixture(t, "normal")
	p, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = p.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	unknown := launchFixture(t, "normal")
	unknown.Bootstrap.Identity.Scope.Service = "ai"
	if _, err = Start(context.Background(), unknown); err == nil {
		t.Fatal("probe falsely claimed an unimplemented domain")
	}
}
