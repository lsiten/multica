package modelservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type fakeManager struct {
	mu         sync.Mutex
	root       string
	server     *httptest.Server
	leases     int
	status     jevmodels.Status
	generation int
	cancels    []context.CancelFunc
	workers    sync.WaitGroup
}

func newFake(root string) *fakeManager {
	m := &fakeManager{root: root, status: jevmodels.Status{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, State: "installed", Installed: true}}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-python-token" {
			w.WriteHeader(401)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) == `{"wait":true}` {
			os.WriteFile(filepath.Join(root, "inference-started"), []byte("started"), 0600)
			timer := time.NewTicker(10 * time.Millisecond)
			defer timer.Stop()
			for {
				if _, err := os.Stat(filepath.Join(root, "inference-release")); err == nil {
					break
				}
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"answers":{}}`)
	}))
	return m
}
func (m *fakeManager) Catalog() []jevmodels.Model { return jevmodels.Catalog() }
func (m *fakeManager) Status(id string, _ ...string) (jevmodels.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != jevmodels.ModelID {
		return jevmodels.Status{}, jevmodels.ErrUnknownModel
	}
	status := m.status
	status.ActiveLeases = m.leases
	return status, nil
}
func (m *fakeManager) Register(context.Context, string, string, *http.Client) (jevmodels.Model, error) {
	return jevmodels.Catalog()[0], nil
}
func (m *fakeManager) Acquire(ctx context.Context, selection jevmodels.Selection) (ModelLease, error) {
	if selection.Device == "mps" {
		os.WriteFile(filepath.Join(m.root, "acquire-started"), []byte("loading"), 0600)
		<-ctx.Done()
		return ModelLease{}, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if selection.ModelID != jevmodels.ModelID {
		return ModelLease{}, jevmodels.ErrUnknownModel
	}
	if m.leases > 0 && selection.Device != "cpu" {
		return ModelLease{}, jevmodels.ErrBusy
	}
	m.leases++
	var once sync.Once
	return ModelLease{Endpoint: m.server.URL, Token: "private-python-token", Device: "cpu", Release: func() { once.Do(func() { m.mu.Lock(); m.leases--; m.mu.Unlock() }) }}, nil
}
func (m *fakeManager) StartInstall(ctx context.Context, id string, _ ...string) (<-chan error, error) {
	if id != jevmodels.ModelID {
		return nil, jevmodels.ErrUnknownModel
	}
	m.mu.Lock()
	m.generation++
	generation := m.generation
	life, cancel := context.WithCancel(ctx)
	m.cancels = append(m.cancels, cancel)
	m.status.Installed = false
	m.status.State = "downloading"
	m.mu.Unlock()
	result := make(chan error, 1)
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-life.Done():
				time.Sleep(80 * time.Millisecond)
				result <- life.Err()
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(m.root, "install-finish")); err == nil {
					m.mu.Lock()
					if generation == m.generation {
						m.status.Installed = true
						m.status.State = "installed"
					}
					m.mu.Unlock()
					result <- nil
					return
				}
			}
		}
	}()
	return result, nil
}
func (m *fakeManager) CancelInstall(string, ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.status.State = "not_installed"
	m.status.Installed = false
	return nil
}
func (m *fakeManager) Stop(string, ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases > 0 {
		return jevmodels.ErrBusy
	}
	m.status.State = "stopped"
	return nil
}
func (m *fakeManager) Remove(string, ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases > 0 {
		return jevmodels.ErrBusy
	}
	m.status.State = "not_installed"
	m.status.Installed = false
	return nil
}
func (m *fakeManager) Close() error {
	m.CancelInstall("")
	m.workers.Wait()
	m.server.Close()
	return nil
}
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == runtimeproc.Entrypoint {
		bootstrap, err := runtimeproc.ReadBootstrap(os.Stdin, "fixture")
		if err != nil {
			os.Exit(91)
		}
		if os.Getenv("MULTICA_TOKEN") != "" {
			os.Exit(92)
		}
		if os.Getenv("MODEL_FIXTURE") == "fake" {
			manager := newFake(bootstrap.Root)
			manager.status.Installed = false
			manager.status.State = "not_installed"
			service, err := newService(bootstrap, manager)
			if err != nil {
				os.Exit(93)
			}
			service.leaseTTL = 300 * time.Millisecond
			if err = service.serve(context.Background()); err != nil {
				os.Exit(94)
			}
		} else {
			if err = Run(context.Background(), bootstrap); err != nil {
				os.Exit(95)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(t *testing.T, fake bool) (*Client, runtimeproc.Bootstrap) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(root, 0700)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "http://fixture.invalid", Account: "fixture-user", Profile: "", DaemonID: "daemon", Service: "ai"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	environment := map[string]string{"MULTICA_JEV_PYTHON": "/missing-python"}
	if fake {
		environment["MODEL_FIXTURE"] = "fake"
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(digest[:]), Environment: environment, Bootstrap: bootstrap, StartupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newClient(process)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client, bootstrap
}
func execution() Execution {
	return Execution{WorkspaceID: "workspace", RuntimeID: "runtime", TaskID: "task", DispatchedAt: "2026-10-08T00:00:00.123456Z"}
}
func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("observable state did not converge")
}
func TestRealManagerChildCatalogCacheLockAndNoImplicitDownload(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "must-not-reach-child")
	client, b := fixture(t, false)
	models, err := client.Catalog(t.Context())
	if err != nil || len(models) == 0 {
		t.Fatalf("catalog %v %v", models, err)
	}
	status, err := client.Status(t.Context(), jevmodels.ModelID, jevmodels.Revision)
	if err != nil || status.Installed {
		t.Fatalf("status %+v %v", status, err)
	}
	if _, err = jevmodels.New(t.Context(), jevmodels.Config{RootDir: b.Root, PythonPath: "missing"}); !errors.Is(err, jevmodels.ErrBusy) {
		t.Fatalf("second cache owner accepted %v", err)
	}
	for _, id := range []string{jevmodels.ModelID, "unknown/model"} {
		_, err = client.Acquire(t.Context(), jevmodels.Selection{ModelID: id, Revision: jevmodels.Revision, Device: "cpu"}, execution())
		if !errors.Is(err, jevmodels.ErrNotInstalled) && !errors.Is(err, jevmodels.ErrUnknownModel) {
			t.Fatalf("acquire %s: %v", id, err)
		}
	}
	if _, err = os.Stat(filepath.Join(b.Root, "engine-"+jevmodels.EngineVersion)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("acquire installed an engine")
	}
}
func TestLeaseProxyExpirationInflightAndLateRelease(t *testing.T) {
	client, b := fixture(t, true)
	selection := jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "cpu"}
	leaseCtx, cancelRenew := context.WithCancel(t.Context())
	defer cancelRenew()
	lease, err := client.Acquire(leaseCtx, selection, execution())
	if err != nil {
		t.Fatal(err)
	}
	if lease.Token == "private-python-token" {
		t.Fatal("Python token crossed IPC")
	}
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest("POST", lease.Endpoint+"/v1/systemone", strings.NewReader(`{"wait":true}`))
		req.Header.Set("Authorization", "Bearer "+lease.Token)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				err = fmt.Errorf("inference %d", res.StatusCode)
			}
		}
		done <- err
	}()
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(b.Root, "inference-started")); return err == nil })
	cancelRenew()
	// A read probe after the finite owner deadline proves expiration without renewal.
	waitFor(t, func() bool {
		req, _ := http.NewRequest("POST", lease.Endpoint+"/v1/systemone", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+lease.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		res.Body.Close()
		return res.StatusCode == 401
	})
	if err = client.Stop(t.Context(), selection.ModelID, selection.Revision); !errors.Is(err, jevmodels.ErrBusy) {
		t.Fatalf("expired lease killed active inference: %v", err)
	}
	if err = lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b.Root, "inference-release"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inference did not finish")
	}
	second, err := client.Acquire(t.Context(), selection, execution())
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(t.Context(), selection.ModelID, selection.Revision)
	if err != nil || status.ActiveLeases != 1 {
		t.Fatalf("old release decremented new lease: %+v %v", status, err)
	}
	if err = second.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestInstallCancelRetryPersistsAttemptAndRejectsOldCancel(t *testing.T) {
	client, b := fixture(t, true)
	if err := client.StartInstall(t.Context(), jevmodels.ModelID, jevmodels.Revision); err != nil {
		t.Fatal(err)
	}
	var old InstallJob
	if err := client.read(t.Context(), "model.job", modelRequest{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision}, &old); err != nil {
		t.Fatal(err)
	}
	if err := client.CancelInstall(t.Context(), jevmodels.ModelID, jevmodels.Revision); err != nil {
		t.Fatal(err)
	}
	if err := client.StartInstall(t.Context(), jevmodels.ModelID, jevmodels.Revision); err != nil {
		t.Fatal(err)
	}
	err := client.mutate(t.Context(), "model.cancel", modelRequest{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, JobID: old.ID}, nil)
	var p *runtimeproc.Error
	if !errors.As(err, &p) || p.Code != "stale_job" {
		t.Fatalf("old cancel accepted %v", err)
	}
	os.WriteFile(filepath.Join(b.Root, "install-finish"), []byte("finish"), 0600)
	waitFor(t, func() bool {
		var current InstallJob
		err := client.read(t.Context(), "model.job", modelRequest{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision}, &current)
		return err == nil && current.ID != old.ID && current.State == "installed"
	})
	inv, err := readInventory(b.Root)
	if err != nil || inv.Jobs[jevmodels.ModelID+"@"+jevmodels.Revision].State != "installed" {
		t.Fatal("install result was not durable")
	}
}
func TestCatalogAndMutationsSurviveReceiptRetirement(t *testing.T) {
	client, _ := fixture(t, true)
	for range 140 {
		if err := client.Stop(t.Context(), jevmodels.ModelID, jevmodels.Revision); err != nil {
			t.Fatal(err)
		}
	}
	models, err := client.Catalog(t.Context())
	if err != nil || len(models) != 1 {
		t.Fatal("long-lived service exhausted receipts")
	}
}

func (m *fakeManager) CloseWithReceipt(receipt func() error) error {
	if err := m.Close(); err != nil {
		return err
	}
	return receipt()
}

func TestUncertainResponseBlocksLaterAcknowledgement(t *testing.T) {
	for _, lost := range []string{"model.stop", "acknowledge"} {
		t.Run(lost, func(t *testing.T) {
			client, b := fixture(t, true)
			record, err := runtimeproc.ReadRecord(b.Root, b.Identity)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			calls := 0
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request runtimeproc.Request
				if err = json.Unmarshal(raw, &request); err != nil {
					t.Error(err)
					return
				}
				req, _ := http.NewRequestWithContext(r.Context(), "POST", record.Address+"/rpc", strings.NewReader(string(raw)))
				req.Header = r.Header.Clone()
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				mu.Lock()
				calls++
				mu.Unlock()
				if request.Operation == lost {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(res.StatusCode)
				w.Write(body)
			}))
			defer proxy.Close()
			proxied := record
			proxied.Address = proxy.URL
			client.transport, err = runtimeproc.NewClient(proxied)
			if err != nil {
				t.Fatal(err)
			}
			err = client.Stop(t.Context(), jevmodels.ModelID, jevmodels.Revision)
			var uncertain *UncertainOperation
			if !errors.As(err, &uncertain) || uncertain.RequestID == "" {
				t.Fatalf("request identity lost: %v", err)
			}
			if (uncertain.Completed != nil) != (lost == "acknowledge") {
				t.Fatal("known domain completion confused with unknown outcome")
			}
			receipt, err := client.QueryOperation(t.Context(), uncertain.RequestID)
			if err != nil || receipt.State != "completed" {
				t.Fatalf("lost response not queryable: %+v %v", receipt, err)
			}
			mu.Lock()
			before := calls
			mu.Unlock()
			if err = client.Remove(t.Context(), jevmodels.ModelID, jevmodels.Revision); !errors.As(err, &uncertain) {
				t.Fatal("unknown result did not fence subsequent mutation")
			}
			mu.Lock()
			after := calls
			mu.Unlock()
			if before != after {
				t.Fatal("later mutation silently acknowledged unknown operation")
			}
		})
	}
}
func TestAdmissionDeadlineAndEmbeddedGrantValidation(t *testing.T) {
	client, _ := fixture(t, true)
	client.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := client.Stop(ctx, jevmodels.ModelID, jevmodels.Revision)
	<-client.gate
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("admission ignored caller deadline: %v", err)
	}
	selection := jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "cpu"}
	valid := grant{Device: "cpu", InstanceID: client.instanceID, LeaseID: strings.Repeat("a", 64), Token: strings.Repeat("b", 64), Execution: execution(), Selection: selection, Deadline: time.Now().Add(time.Minute), Endpoint: "http://127.0.0.1:1234/leases/" + strings.Repeat("a", 64)}
	if err = client.validateGrant(valid, selection, execution()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"instance", "claim", "selection", "endpoint", "token"} {
		bad := valid
		switch field {
		case "instance":
			bad.InstanceID = "old"
		case "claim":
			bad.Execution.DispatchedAt = "2020-01-01T00:00:00Z"
		case "selection":
			bad.Selection.Device = "cuda"
		case "endpoint":
			bad.Endpoint = "https://attacker.invalid/model"
		case "token":
			bad.Token = "invalid"
		}
		if err = client.validateGrant(bad, selection, execution()); err == nil {
			t.Fatalf("bad %s grant accepted", field)
		}
	}
}
func TestSlowAcquireDoesNotStarveExistingLeaseRenewal(t *testing.T) {
	client, b := fixture(t, true)
	lease, err := client.Acquire(t.Context(), jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "cpu"}, execution())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.Acquire(ctx, jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "mps"}, execution())
		done <- err
	}()
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(b.Root, "acquire-started")); return err == nil })
	started := time.Now()
	waitFor(t, func() bool { return time.Since(started) > 600*time.Millisecond })
	req, _ := http.NewRequest("POST", lease.Endpoint+"/v1/systemone", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+lease.Token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("model loading starved lease renewal")
	}
	select {
	case err = <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("slow acquire cancellation %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow acquire did not cancel")
	}
	if err = lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestExplicitEngineEnvironmentPreservesCompatibilityWithoutAccountCredentials(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH"} {
		t.Setenv(key, "synthetic-test-only")
	}
	t.Setenv("MULTICA_TOKEN", "forbidden-account-fixture")
	t.Setenv("UNRELATED_SECRET", "forbidden-unrelated-fixture")
	env := modelEnvironment()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH"} {
		if env[key] != "synthetic-test-only" {
			t.Fatalf("explicit engine setting missing: %s", key)
		}
	}
	if _, ok := env["MULTICA_TOKEN"]; ok {
		t.Fatal("account token inherited")
	}
	if _, ok := env["UNRELATED_SECRET"]; ok {
		t.Fatal("unrelated secret inherited")
	}
}
func TestNoDescendantCrashRecoveryAndUnknownDescendantRefusal(t *testing.T) {
	for _, possible := range []bool{false, true} {
		t.Run(fmt.Sprint(possible), func(t *testing.T) {
			client, b := fixture(t, false)
			if possible {
				_, err := client.Acquire(t.Context(), jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "cpu"}, execution())
				if !errors.Is(err, jevmodels.ErrNotInstalled) {
					t.Fatal(err)
				}
			}
			if err := client.process.Close(); err != nil {
				t.Fatal(err)
			}
			// The explicit crash has already reaped this fixture; suppress only its normal-stop cleanup.
			client.closeOnce.Do(func() {})
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := Launch(context.Background(), b.Root, b.Identity.Scope, executable, "fixture")
			if possible {
				if err == nil {
					replacement.Close()
					t.Fatal("unknown descendants were treated as stopped")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			archived, err := runtimeproc.ReadReconciledRecord(b.Root, b.Identity)
			if err != nil || len(archived.Reconciliation) == 0 {
				t.Fatal("recovery did not preserve old owner inventory")
			}
		})
	}
}
