package runtimeproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testBootstrap(t *testing.T) Bootstrap {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := NewIdentity(Scope{Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func startService(t *testing.T, cfg Config) (*Service, *Client, context.CancelFunc, <-chan error) {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("service failed to join")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := Open(ctx, cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
		if err == nil {
			return s, c, cancel, done
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("service never became ready")
	return nil, nil, nil, nil
}
func request(t *testing.T, c *Client, op string, payload string) Request {
	t.Helper()
	status, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Request(op, status.Fence, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func call(t *testing.T, c *Client, r Request) Response {
	t.Helper()
	out, err := c.Call(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func wantCode(t *testing.T, c *Client, r Request, code string) {
	t.Helper()
	_, err := c.Call(context.Background(), r)
	var problem *Error
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("error=%v want=%s", err, code)
	}
}
func TestAuthenticatedMutationReceiptsAndFencing(t *testing.T) {
	b := testBootstrap(t)
	var calls atomic.Int32
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"increment"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) {
		return json.RawMessage(fmt.Sprintf(`{"count":%d}`, calls.Add(1))), nil
	}})
	r := request(t, c, "increment", `{"delta":1}`)
	out := call(t, c, r)
	if out.Receipt.State != "completed" || out.Status.Fence.Revision != 2 {
		t.Fatalf("bad receipt %+v", out)
	}
	disk, err := ReadRecord(b.Root, b.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if disk.Operations[r.RequestID].State != "completed" {
		t.Fatal("success ACK preceded durable receipt")
	}
	if got := call(t, c, r); string(got.Receipt.Result) != `{"count":1}` || calls.Load() != 1 {
		t.Fatal("duplicate replayed")
	}
	receipt, err := c.QueryOperation(context.Background(), r.RequestID)
	if err != nil || receipt.State != "completed" {
		t.Fatalf("query=%+v %v", receipt, err)
	}
	changed := r
	changed.Payload = json.RawMessage(`{"delta":2}`)
	wantCode(t, c, changed, "request_conflict")
	old := r
	old.RequestID = "1:old-revision"
	wantCode(t, c, old, "stale_fence")
	for _, field := range []string{"backend", "account", "profile", "daemon", "service", "instance", "build", "protocol"} {
		t.Run(field, func(t *testing.T) {
			other := request(t, c, "increment", "{}")
			switch field {
			case "backend":
				other.Identity.Scope.Backend = "other"
			case "account":
				other.Identity.Scope.Account = "other"
			case "profile":
				other.Identity.Scope.Profile = "other"
			case "daemon":
				other.Identity.Scope.DaemonID = "other"
			case "service":
				other.Identity.Scope.Service = "other"
			case "instance":
				other.Identity.InstanceID = strings.Repeat("0", 32)
			case "build":
				other.Identity.Build = "other"
			case "protocol":
				other.Identity.Protocol++
			}
			wantCode(t, c, other, "identity_mismatch")
		})
	}
	for _, field := range []string{"supervisor", "resource"} {
		other := request(t, c, "increment", "{}")
		if field == "supervisor" {
			other.Fence.SupervisorEpoch++
		} else {
			other.Fence.ResourceEpoch++
		}
		wantCode(t, c, other, "stale_fence")
	}
	expired := request(t, c, "increment", "{}")
	expired.Deadline = time.Now().Add(-time.Second)
	if _, err := c.Call(context.Background(), expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired client deadline lost: %v", err)
	}
	unknown := request(t, c, "missing", "{}")
	wantCode(t, c, unknown, "unknown_operation")
	badRecord := c.record
	badRecord.Token = strings.Repeat("0", 64)
	bad, err := NewClient(badRecord)
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, bad, r, "unauthorized")
	if calls.Load() != 1 {
		t.Fatalf("rejected requests executed handler: %d", calls.Load())
	}
}
func TestDrainResumeAndStop(t *testing.T) {
	b := testBootstrap(t)
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"change"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return json.RawMessage(`{}`), nil }})
	drained := call(t, c, request(t, c, "drain", ""))
	if drained.Status.State != "draining" {
		t.Fatal("not draining")
	}
	if _, err := c.Handshake(context.Background()); err == nil {
		t.Fatal("draining reported ready")
	}
	wantCode(t, c, request(t, c, "change", "{}"), "draining")
	resumed := call(t, c, request(t, c, "resume", ""))
	if resumed.Status.State != "ready" {
		t.Fatal("not resumed")
	}
	stopped := call(t, c, request(t, c, "stop", ""))
	if stopped.Status.State != "stopped" || stopped.Receipt.State != "completed" {
		t.Fatal("stop not durable")
	}
}
func TestCanceledHandlerRemainsUncertainAndDoesNotReplay(t *testing.T) {
	b := testBootstrap(t)
	var count atomic.Int32
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"wait"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
		count.Add(1)
		<-ctx.Done()
		return nil, nil
	}})
	r := request(t, c, "wait", "{}")
	r.Deadline = time.Now().Add(50 * time.Millisecond)
	if _, err := c.Call(context.Background(), r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled client deadline lost: %v", err)
	}
	receipt, err := c.QueryOperation(context.Background(), r.RequestID)
	if err != nil || receipt.State != "pending" {
		t.Fatal("uncertain operation not queryable")
	}
	r.Deadline = time.Now().Add(time.Second)
	wantCode(t, c, r, "request_conflict")
	if count.Load() != 1 {
		t.Fatal("uncertain operation replayed")
	}
}
func TestMalformedAndOversizedBodies(t *testing.T) {
	_, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t)})
	for _, body := range []string{`{`, `{} {}`, strings.Repeat("x", maxBody+1)} {
		req, err := http.NewRequest("POST", c.record.Address+"/rpc", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.record.Token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		want := 400
		if len(body) > maxBody {
			want = 413
		}
		if response.StatusCode != want {
			t.Fatalf("status=%d want=%d", response.StatusCode, want)
		}
	}
}
func TestOwnerAndStorageProtection(t *testing.T) {
	b := testBootstrap(t)
	_, _, _, _ = startService(t, Config{Bootstrap: b})
	if _, err := NewService(Config{Bootstrap: b}); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	if _, err := ReadRecord(b.Root, Identity{Scope: b.Identity.Scope}); err == nil {
		t.Fatal("incomplete identity accepted")
	}
	path := RecordPath(b.Root, b.Identity.Scope)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecord(b.Root, b.Identity); err == nil {
		t.Fatal("public credential record accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://example.com:80", "http://localhost:8080", "http://127.0.0.1:80/path", "http://127.0.0.1:0", "http://user@127.0.0.1:80", "https://127.0.0.1:80"} {
		r := Record{Identity: b.Identity, Token: b.Token, Address: origin}
		if _, err := NewClient(r); err == nil {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
}
func TestUnconfirmedPreviousOwnerAndPendingReceiptCannotBeReplaced(t *testing.T) {
	for _, state := range []string{"ready", "suspect", "starting", "stopped-pending"} {
		t.Run(state, func(t *testing.T) {
			b := testBootstrap(t)
			dir, err := prepareDirectory(b.Root, b.Identity.Scope)
			if err != nil {
				t.Fatal(err)
			}
			record := Record{Identity: b.Identity, Token: b.Token, State: state, Fence: b.Fence, Operations: map[string]Receipt{}}
			if state == "stopped-pending" {
				record.State = "stopped"
				record.Operations["uncertain"] = Receipt{State: "pending"}
			}
			if err = writeRecord(filepath.Join(dir, "owner.json"), record); err != nil {
				t.Fatal(err)
			}
			if _, err = NewService(Config{Bootstrap: b}); err == nil {
				t.Fatal("unconfirmed previous owner replaced")
			}
		})
	}
}
func TestSymlinkRecordsAndRootsRejected(t *testing.T) {
	b := testBootstrap(t)
	dir, err := prepareDirectory(b.Root, b.Identity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(b.Root, "target")
	if err = os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "owner.json")
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadRecord(b.Root, b.Identity); err == nil {
		t.Fatal("symlink record accepted")
	}
	if _, err = NewService(Config{Bootstrap: b}); err == nil {
		t.Fatal("symlink record overwritten")
	}
	alias := filepath.Join(b.Root, "alias")
	if err = os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareDirectory(alias, b.Identity.Scope); err == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestPendingRefusesAcknowledgementAndStorageFailureNeverACKs(t *testing.T) {
	b := testBootstrap(t)
	path := RecordPath(b.Root, b.Identity.Scope)
	backup := path + ".fixture-backup"
	var calls atomic.Int32
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"change"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) {
		calls.Add(1)
		if err := os.Rename(path, backup); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Error(err)
		}
		return json.RawMessage(`{"applied":true}`), nil
	}})
	r := request(t, c, "change", "{}")
	wantCode(t, c, r, "storage")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	receipt, err := c.QueryOperation(context.Background(), r.RequestID)
	if err != nil || receipt.State != "pending" {
		t.Fatalf("undurable outcome exposed: %+v %v", receipt, err)
	}
	disk, err := ReadRecord(b.Root, b.Identity)
	if err != nil || disk.Operations[r.RequestID].State != "pending" {
		t.Fatal("disk intent not preserved")
	}
	wantCode(t, c, request(t, c, "acknowledge", ""), "uncertain_operation")
	if calls.Load() != 1 {
		t.Fatal("domain executed more than once")
	}
}
func TestReadinessFailureCleansDomainAndKeepsRecordNotReady(t *testing.T) {
	b := testBootstrap(t)
	var closed atomic.Bool
	s, err := NewService(Config{Bootstrap: b, Ready: func(context.Context) error { return errors.New("fixture dependency unavailable") }, Shutdown: func(context.Context) error { closed.Store(true); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Serve(context.Background()); err == nil || !closed.Load() {
		t.Fatal("failed readiness did not clean its domain")
	}
	record, err := ReadRecord(b.Root, b.Identity)
	if err != nil || record.State != "stopped" {
		t.Fatalf("readiness falsely announced %+v %v", record, err)
	}
}
func TestDefaultProfileIsolationAndCanceledClient(t *testing.T) {
	b := testBootstrap(t)
	b.Identity.Scope.Profile = ""
	_, c, _, _ := startService(t, Config{Bootstrap: b})
	named := b.Identity
	named.Scope.Profile = "default"
	if _, err := ReadRecord(b.Root, named); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default profile collided: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Health(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation erased: %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); w.WriteHeader(200) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	b := testBootstrap(t)
	client, err := NewClient(Record{Identity: b.Identity, Token: b.Token, Address: redirect.URL, ReplayEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Health(context.Background()); err == nil || reached.Load() {
		t.Fatal("credential followed redirect")
	}
}

func TestStoppedInstanceRequiresFreshIdentity(t *testing.T) {
	b := testBootstrap(t)
	s, err := NewService(Config{Bootstrap: b})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = NewService(Config{Bootstrap: b}); err == nil {
		t.Fatal("same stopped identity reset replay protection")
	}
	identity, err := NewIdentity(b.Identity.Scope, b.Identity.Build)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewBootstrap(b.Root, identity)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := NewService(Config{Bootstrap: fresh})
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.Serve(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDomainQuotaStillAdmitsDrainAndStop(t *testing.T) {
	b := testBootstrap(t)
	s, c, _, _ := startService(t, Config{Bootstrap: b})
	s.mu.Lock()
	for n := range maxOperations {
		s.record.Operations[fmt.Sprintf("1:fixture-%d", n)] = Receipt{RequestID: fmt.Sprintf("1:fixture-%d", n), State: "completed"}
	}
	err := writeRecord(s.path, s.record)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if out := call(t, c, request(t, c, "drain", "")); out.Status.State != "draining" {
		t.Fatal("domain quota blocked drain")
	}
	if out := call(t, c, request(t, c, "stop", "")); out.Status.State != "stopped" {
		t.Fatal("domain quota blocked stop")
	}
}
