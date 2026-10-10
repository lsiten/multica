package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationProcessBackend struct {
	stoppedObservations                 int
	mu                                  sync.Mutex
	command                             protocol.ApplicationControlCommand
	grant                               protocol.ApplicationServiceGrantResponse
	claimed                             bool
	results                             []protocol.ApplicationStepResult
	controlConnections, dataConnections int
	hub                                 *applicationgateway.Hub
	server                              *httptest.Server
}

func newApplicationProcessFixture(t *testing.T, configure ...func(*Daemon, *protocol.ApplicationControlCommand)) (*Daemon, *applicationProcessClient, *applicationProcessBackend) {
	t.Helper()
	backend := &applicationProcessBackend{hub: applicationgateway.NewHub()}
	backend.server = httptest.NewServer(http.HandlerFunc(backend.serve))
	t.Cleanup(backend.server.Close)
	d, command := applicationManagerFixture(t, backend.server.URL)
	canonical, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.WorkspacesRoot = canonical
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.ServerBaseURL = backend.server.URL
	d.cfg.NativeHostExecutable = executable
	d.cfg.NativeHostBuild = "fixture/commit"
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(canonical, "vscreen.json")
	d.cfg.ProcessServices = []string{"application"}
	d.client.SetToken("parent-fixture-token")
	command.Config.Environment["APPLICATION_HOST_PID_FILE"] = filepath.Join(canonical, "host.pid")
	for _, edit := range configure {
		edit(d, &command)
	}
	backend.command = command
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child, err := d.ensureApplicationProcess(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := child.close(); err != nil {
			t.Errorf("application child cleanup: %v", err)
			_ = child.process.Close()
		}
	})
	if err = child.sync(ctx); err != nil {
		t.Fatal(err)
	}
	return d, child, backend
}
func (b *applicationProcessBackend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	if r.URL.Path == "/api/me" {
		b.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer parent-fixture-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "fixture-account"})
		return
	}
	if strings.Contains(r.URL.Path, "application-service-grants") {
		defer b.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer parent-fixture-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			b.grant.Revoked = true
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodGet {
			state := b.grant.ApplicationServiceGrantState
			state.Capability = protocol.ApplicationServiceGrantCapability
			state.RuntimeID = b.command.RuntimeID
			_ = json.NewEncoder(w).Encode(state)
			return
		}
		var input protocol.ApplicationServiceGrantRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.ExpectedGeneration != b.grant.Generation {
			http.Error(w, "stale", 409)
			return
		}
		instance, err := uuid.Parse(input.ServiceInstanceID)
		if err != nil {
			http.Error(w, "bad instance", 400)
			return
		}
		b.grant = protocol.ApplicationServiceGrantResponse{ApplicationServiceGrantState: protocol.ApplicationServiceGrantState{Capability: protocol.ApplicationServiceGrantCapability, WorkspaceID: b.command.WorkspaceID, RuntimeID: b.command.RuntimeID, DaemonID: "66666666-6666-4666-8666-666666666666", ServiceInstanceID: instance.String(), Generation: input.ExpectedGeneration + 1}, Token: "mps_" + strings.Repeat(fmt.Sprint((input.ExpectedGeneration+1)%10), 64), Operations: input.Operations, ExpiresAt: time.Now().Add(15 * time.Minute)}
		_ = json.NewEncoder(w).Encode(b.grant)
		return
	}
	if b.grant.Revoked || r.Header.Get("Authorization") != "Bearer "+b.grant.Token {
		b.mu.Unlock()
		http.Error(w, "scoped token required", 401)
		return
	}
	command := b.command
	if strings.Contains(r.URL.Path, "/tunnel/") {
		if strings.HasSuffix(r.URL.Path, "/control") {
			b.controlConnections++
		} else {
			b.dataConnections++
		}
		b.mu.Unlock()
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/control") {
			_ = b.hub.Control(r.Context(), command.RuntimeID, connection)
			return
		}
		var attachment struct {
			StreamID string `json:"stream_id"`
			Token    string `json:"token"`
		}
		if connection.ReadJSON(&attachment) != nil {
			connection.Close()
			return
		}
		if _, err = b.hub.Attach(command.RuntimeID, command.WorkspaceID, attachment.StreamID, attachment.Token, connection); err != nil {
			connection.Close()
		}
		return
	}
	defer b.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/sync"):
		_ = json.NewEncoder(w).Encode([]protocol.ApplicationRuntimeInstance{{DesiredState: "running", HasPendingOperation: !b.claimed, Command: command}})
	case strings.HasSuffix(r.URL.Path, "/claim"):
		claims := []protocol.ApplicationClaim{}
		if !b.claimed {
			b.claimed = true
			claims = append(claims, protocol.ApplicationClaim{StepID: "77777777-7777-4777-8777-777777777777", OperationID: "88888888-8888-4888-8888-888888888888", ClaimToken: "99999999-9999-4999-8999-999999999999", Command: command})
		}
		_ = json.NewEncoder(w).Encode(claims)
	case strings.HasSuffix(r.URL.Path, "/result"):
		var input struct {
			Result protocol.ApplicationStepResult `json:"result"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		b.results = append(b.results, input.Result)
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/observe"):
		var input struct {
			Observation protocol.ApplicationObservation `json:"observation"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Observation.ProcessState == "stopped" {
			b.stoppedObservations++
		}
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/lease"):
		w.WriteHeader(204)
	default:
		http.NotFound(w, r)
	}
}
func TestApplicationProcessOwnsRealHostAndDirectTunnel(t *testing.T) {
	d, child, backend := newApplicationProcessFixture(t)
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	result := backend.results[0]
	command := backend.command
	backend.mu.Unlock()
	if result.State != "completed" {
		t.Fatalf("child operation failed: %+v", result)
	}
	inventory, err := child.inventory(t.Context())
	if err != nil || inventory.Unknown || inventory.PID == os.Getpid() || len(inventory.Observations) != 1 {
		t.Fatalf("child inventory %+v: %v", inventory, err)
	}
	path, record, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	hostRaw, err := os.ReadFile(command.Config.Environment["APPLICATION_HOST_PID_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	hostPID, err := strconv.Atoi(string(hostRaw))
	if err != nil || hostPID == inventory.PID || hostPID == os.Getpid() {
		t.Fatal("host is not an independent process")
	}
	if _, loaded := d.applicationCommands.Load(command.InstanceID); loaded {
		t.Fatal("parent owns child application commands")
	}
	request, err := http.NewRequest(http.MethodGet, "http://service/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	waitApplicationManager(t, func() bool { return backend.hub.Connected(command.RuntimeID) })
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return backend.hub.Open(ctx, protocol.ApplicationTunnelRequest{EndpointID: "fixture", RuntimeID: command.RuntimeID, WorkspaceID: command.WorkspaceID, InstanceID: command.InstanceID, Generation: command.Generation, Kind: "service", Port: command.Config.Port})
	}}
	defer transport.CloseIdleConnections()
	request = request.WithContext(ctx)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(raw) != "managed application response" {
		t.Fatalf("direct response %q: %v", raw, err)
	}
	backend.mu.Lock()
	controls, data := backend.controlConnections, backend.dataConnections
	backend.mu.Unlock()
	if controls < 1 || data < 1 {
		t.Fatal("no direct child data connection")
	}
	if err = child.close(); err != nil {
		t.Fatal(err)
	}
	if err = applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err = child.inventory(ctx); err == nil {
		t.Fatal("stopped child still responds")
	}
	backend.mu.Lock()
	stoppedReports := backend.stoppedObservations
	backend.mu.Unlock()
	if stoppedReports < 1 {
		t.Fatal("confirmed shutdown omitted stopped observation")
	}
	if _, err = d.executeApplication(ctx, command); err == nil {
		t.Fatal("parent regained application authority")
	}
	t.Logf("controller_pid=%d child_pid=%d host_pid=%d direct_control=%d direct_data=%d result=completed stopped_lock=true", os.Getpid(), inventory.PID, hostPID, controls, data)
}

func TestApplicationProcessRotationJoinsOldRuntimeAndPreservesHost(t *testing.T) {
	d, child, backend := newApplicationProcessFixture(t)
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	command := backend.command
	backend.mu.Unlock()
	_, before, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	grants := child.grantSnapshot()
	grant := grants[command.RuntimeID]
	grant.ExpiresAt = time.Now().Add(time.Minute)
	grants[command.RuntimeID] = grant
	child.storeGrants(grants)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err = child.sync(ctx); err != nil {
		t.Fatal(err)
	}
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return backend.controlConnections >= 2 })
	afterGrant := child.grantSnapshot()[command.RuntimeID]
	if afterGrant.Generation != grant.Generation+1 {
		t.Fatal("renewal did not rotate generation")
	}
	_, after, err := d.applicationRecord(command)
	if err != nil || after.HostID != before.HostID {
		t.Fatal("renewal restarted application host")
	}
	raw, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || strings.Count(string(raw), "start\n") != 1 {
		t.Fatalf("application restarted: %q %v", raw, err)
	}
	if err = child.mutation(ctx, "application.bind", applicationProcessBinding{WorkspacesRoot: d.cfg.WorkspacesRoot, SourceAddress: child.source.address, Grants: []protocol.ApplicationServiceGrantResponse{grant}}); err == nil {
		t.Fatal("stale grant binding accepted")
	}
	if child.uncertain != nil {
		t.Fatal("known stale domain result poisoned transport")
	}
	if err = child.remove(ctx, command.RuntimeID); err != nil {
		t.Fatal(err)
	}
	inventory, err := child.inventory(ctx)
	if err != nil || inventory.Runtimes != 0 {
		t.Fatalf("removed runtime retained: %+v %v", inventory, err)
	}
	path, record, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	if err = applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
		t.Fatal(err)
	}
	t.Logf("grant_generation=%d->%d host_identity_unchanged=true start_count=1 removed_runtime_count=0 stop_lock=true", grant.Generation, afterGrant.Generation)
}

func TestApplicationProcessCrashAdoptsAuthenticatedHostWithoutRestart(t *testing.T) {
	d, child, backend := newApplicationProcessFixture(t)
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	command := backend.command
	backend.mu.Unlock()
	_, before, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := child.bootstrap.Identity
	if err = child.process.Close(); err != nil {
		t.Fatal(err)
	}
	child.source.close()
	child.closeOnce.Do(func() {})
	if health := d.applicationProcessHealth(t.Context()); health == nil || !health.Unknown {
		t.Fatal("dead manager reported healthy")
	}
	host, err := applicationhost.NewClient(before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Status(t.Context()); err != nil {
		t.Fatal("manager crash killed independently owned host")
	}
	d.applicationProcessMu.Lock()
	d.applicationProcess = nil
	d.applicationProcessMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	replacement, err := d.ensureApplicationProcess(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := replacement.close(); err != nil {
			t.Errorf("replacement cleanup: %v", err)
			_ = replacement.process.Close()
		}
	})
	if err = replacement.sync(ctx); err != nil {
		t.Fatal(err)
	}
	waitApplicationManager(t, func() bool {
		inventory, err := replacement.inventory(ctx)
		return err == nil && !inventory.Unknown && len(inventory.Observations) == 1
	})
	_, after, err := d.applicationRecord(command)
	if err != nil || after.HostID != before.HostID {
		t.Fatal("manager replacement changed authenticated host identity")
	}
	raw, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || strings.Count(string(raw), "start\n") != 1 {
		t.Fatalf("replacement duplicated application launch: %q %v", raw, err)
	}
	archive, err := runtimeproc.ReadReconciledRecord(child.bootstrap.Root, oldIdentity)
	if err != nil || archive.Identity != oldIdentity {
		t.Fatalf("old journal not preserved: %v", err)
	}
	t.Logf("old_instance=%s new_instance=%s same_host=%s start_count=1 old_journal_archived=true", oldIdentity.InstanceID, replacement.bootstrap.Identity.InstanceID, after.HostID)
}

func TestApplicationProcessUnknownOwnershipRefusesReplacement(t *testing.T) {
	d, child, backend := newApplicationProcessFixture(t)
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := d.reconcileApplicationManager(ctx, child.bootstrap.Root, child.bootstrap.Identity.Scope); err == nil {
		t.Fatal("live manager ownership was retired")
	}
	backend.mu.Lock()
	command := backend.command
	backend.mu.Unlock()
	_, running, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	if err = child.process.Close(); err != nil {
		t.Fatal(err)
	}
	child.source.close()
	child.closeOnce.Do(func() {})
	unknown := command
	unknown.InstanceID = uuid.NewString()
	directory, err := d.applicationDirectory(unknown)
	if err != nil {
		t.Fatal(err)
	}
	record, err := applicationhost.NewRecord(unknown, running.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "host.json")
	if err = applicationhost.WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err = d.reconcileApplicationManager(ctx, child.bootstrap.Root, child.bootstrap.Identity.Scope); err == nil {
		t.Fatal("unconfirmed independent host allowed manager replacement")
	}
	preserved, err := runtimeproc.InspectRecord(child.bootstrap.Root, child.bootstrap.Identity.Scope)
	if err != nil || preserved.Identity != child.bootstrap.Identity || preserved.State == "stopped" {
		t.Fatalf("unknown manager record altered: %v", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	host, err := applicationhost.NewClient(running)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Stop(ctx, command.Generation); err != nil {
		t.Fatal(err)
	}
	runningPath, _, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	if err = applicationhost.WaitStopped(ctx, runningPath, running.HostID); err != nil {
		t.Fatal(err)
	}
	t.Log("live manager lock denied retirement; unknown host denied replacement; original identity preserved; independent known host explicitly stopped")
}

func TestApplicationProcessLostResponseRetainsExactRequest(t *testing.T) {
	_, child, backend := newApplicationProcessFixture(t)
	original := child.process.Client
	record, err := runtimeproc.ReadRecord(child.bootstrap.Root, child.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	originalAddress := record.Address
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		inputRaw, _ := io.ReadAll(r.Body)
		var input runtimeproc.Request
		_ = json.Unmarshal(inputRaw, &input)
		request, err := http.NewRequestWithContext(r.Context(), r.Method, originalAddress+r.URL.Path, bytes.NewReader(inputRaw))
		if err != nil {
			t.Error(err)
			return
		}
		request.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			http.Error(w, "upstream failed", 502)
			return
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var answer runtimeproc.Response
		_ = json.Unmarshal(raw, &answer)
		if answer.Receipt != nil && input.Operation == "application.wake" {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		for name, values := range response.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(raw)
	}))
	defer proxy.Close()
	record.Address = proxy.URL
	client, err := runtimeproc.NewClient(record)
	if err != nil {
		t.Fatal(err)
	}
	child.process.Client = client
	defer func() { child.process.Client = original }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = child.mutation(ctx, "application.wake", nil)
	var uncertain *applicationUncertainOperation
	if !errors.As(err, &uncertain) || uncertain.RequestID == "" {
		t.Fatalf("lost result missing exact identity: %v", err)
	}
	priorCalls := calls.Load()
	if err = child.mutation(ctx, "application.remove", map[string]string{"runtime_id": "other"}); !errors.As(err, &uncertain) {
		t.Fatal("unknown outcome silently retried")
	}
	if calls.Load() != priorCalls {
		t.Fatal("next mutation or ACK crossed unresolved outcome")
	}
	receipt, err := original.QueryOperation(ctx, uncertain.RequestID)
	if err != nil || receipt.State != "completed" {
		t.Fatalf("unconsumed outcome retired: %+v %v", receipt, err)
	}
	grants := child.grantSnapshot()
	for id, g := range grants {
		g.ExpiresAt = time.Now().Add(4 * time.Minute)
		grants[id] = g
	}
	child.storeGrants(grants)
	backend.mu.Lock()
	generation := backend.grant.Generation
	backend.mu.Unlock()
	if err = child.sync(ctx); !errors.As(err, &uncertain) {
		t.Fatal("controller discarded unresolved operation before grant renewal")
	}
	backend.mu.Lock()
	currentGeneration := backend.grant.Generation
	backend.mu.Unlock()
	if currentGeneration != generation {
		t.Fatal("unresolved operation silently rotated backend authority")
	}
	t.Logf("lost_request_id=%s durable_state=%s subsequent_requests=0", uncertain.RequestID, receipt.State)
}

func TestApplicationProcessServiceEnvFixture(t *testing.T) {
	if os.Getenv("APPLICATION_PROCESS_EXPECT_LOCAL") != "1" {
		return
	}
	if os.Getenv("APPLICATION_DECLARED_SECRET") != "synthetic-fixture-value" || os.Getenv("APPLICATION_UNDECLARED_SECRET") != "" || os.Getenv("MULTICA_TOKEN") != "" {
		t.Fatal("declared environment boundary failed")
	}
	TestApplicationDaemonServiceFixture(t)
}
func TestApplicationProcessSourceOnlyResolvesDeclaredEnvironment(t *testing.T) {
	t.Setenv("APPLICATION_LOCAL_SOURCE", "synthetic-fixture-value")
	t.Setenv("APPLICATION_UNDECLARED_SECRET", "must-not-inherit")
	t.Setenv("MULTICA_TOKEN", "synthetic-parent-only")
	_, child, backend := newApplicationProcessFixture(t, func(d *Daemon, command *protocol.ApplicationControlCommand) {
		command.Config.Command[1] = "-test.run=^TestApplicationProcessServiceEnvFixture$"
		command.Config.Environment["APPLICATION_PROCESS_EXPECT_LOCAL"] = "1"
		command.Config.LocalEnv = map[string]string{"APPLICATION_DECLARED_SECRET": "APPLICATION_LOCAL_SOURCE"}
	})
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	result := backend.results[0]
	command := backend.command
	backend.mu.Unlock()
	if result.State != "completed" {
		t.Fatalf("declared environment child failed: %s", result.Error)
	}
	source := applicationSourceInput(command)
	input := applicationSourceEnvelope{InstanceID: child.bootstrap.Identity.InstanceID, GrantGeneration: 999, Deadline: time.Now().Add(time.Minute), Source: source}
	raw, _ := json.Marshal(input)
	request, _ := http.NewRequest(http.MethodPost, child.source.address+"/source", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+child.bootstrap.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("stale source grant accepted: %d", response.StatusCode)
	}
	source.LocalEnv = map[string]string{"APP_ALIAS": "MULTICA_TOKEN"}
	input.Source = source
	input.GrantGeneration = child.grantSnapshot()[command.RuntimeID].Generation
	raw, _ = json.Marshal(input)
	request, _ = http.NewRequest(http.MethodPost, child.source.address+"/source", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+child.bootstrap.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("reserved account environment accepted: %d", response.StatusCode)
	}
	t.Log("declared synthetic LocalEnv delivered; undeclared/account environment absent; stale source generation403; reserved source declaration409")
}

func TestApplicationProcessRevocationJoinsPreparingHost(t *testing.T) {
	var marker string
	d, child, backend := newApplicationProcessFixture(t, func(d *Daemon, command *protocol.ApplicationControlCommand) {
		marker = filepath.Join(d.cfg.WorkspacesRoot, "preparation-entered")
		command.Config.Environment["APPLICATION_SCOPE_PREPARATION_MARKER"] = marker
		command.Config.Prepare = []protocol.ApplicationCommand{{Args: []string{command.Config.Command[0], "-test.run=^TestApplicationScopePreparationFixture$"}, TimeoutSeconds: 30}}
	})
	waitApplicationManager(t, func() bool { _, err := os.Stat(marker); return err == nil })
	backend.mu.Lock()
	command := backend.command
	backend.mu.Unlock()
	d.mu.Lock()
	delete(d.workspaces, command.WorkspaceID)
	delete(d.runtimeIndex, command.RuntimeID)
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	d.stopUnavailableWorkspaceApplications(ctx, map[string]string{})
	inventory, err := child.inventory(ctx)
	if err != nil || inventory.Runtimes != 0 {
		t.Fatalf("revoked child runtime still active: %+v %v", inventory, err)
	}
	path, record, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	if err = applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(command.Config.Environment["APPLICATION_START_FILE"]); !os.IsNotExist(err) {
		t.Fatalf("revoked preparation launched service: %v", err)
	}
	t.Log("real child preparation canceled and joined; host stopped ownership confirmed; application start count0")
}

func TestApplicationProcessRejectedBindingCanReconcileSameGrant(t *testing.T) {
	_, child, backend := newApplicationProcessFixture(t)
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	generation := backend.grant.Generation
	backend.mu.Unlock()
	address := child.source.address
	child.source.address = "http://127.0.0.1:1"
	child.bindingReady = false
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := child.sync(ctx)
	child.source.address = address
	if err == nil || child.uncertain != nil {
		t.Fatalf("explicit invalid binding was not safely rejected: %v", err)
	}
	if err = child.sync(ctx); err != nil || !child.bindingReady {
		t.Fatalf("same grant did not reconcile after rejected bind: %v", err)
	}
	backend.mu.Lock()
	current := backend.grant.Generation
	backend.mu.Unlock()
	if current != generation {
		t.Fatal("known bind rejection rotated credentials unnecessarily")
	}
	child.control <- struct{}{}
	short, stop := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer stop()
	err = child.remove(short, backend.command.RuntimeID)
	<-child.control
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("controller admission ignored context: %v", err)
	}
	t.Log("known bind rejection reconciled with same grant; waiting controller admission honored deadline")
}
