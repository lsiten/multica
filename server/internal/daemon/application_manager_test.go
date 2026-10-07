package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationDaemonHostFixture(t *testing.T) {
	for index, arg := range os.Args {
		if arg == applicationhost.Entrypoint && index+1 < len(os.Args) {
			record, err := applicationhost.ReadRecord(os.Args[index+1])
			if err != nil {
				t.Fatal(err)
			}
			if gateway := record.Command.Config.Environment["APPLICATION_FIXTURE_GATEWAY_ADDR"]; gateway != "" {
				transport := http.DefaultTransport.(*http.Transport).Clone()
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(address)
					if err == nil && strings.HasSuffix(host, ".apps.localhost") {
						address = gateway
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}
				http.DefaultTransport = transport
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			if err := applicationhost.Run(ctx, os.Args[index+1]); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}

func TestApplicationDaemonServiceFixture(t *testing.T) {
	if os.Getenv("APPLICATION_DAEMON_FIXTURE") != "1" {
		return
	}
	if os.Getenv("MULTICA_TOKEN") != "" || os.Getenv("MULTICA_TASK_TOKEN") != "" {
		t.Fatal("management credential reached a service")
	}
	stream, err := os.OpenFile(os.Getenv("APPLICATION_START_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.WriteString("start\n"); err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "application service started"); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("PORT"), ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/dependency" || strings.HasPrefix(r.URL.Path, "/dependency/")) && os.Getenv("API_URL") != "" {
			target, err := url.Parse(os.Getenv("API_URL"))
			if err != nil {
				http.Error(w, "dependency URL is invalid", http.StatusBadGateway)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
					http.Error(w, "fixture streaming unavailable", http.StatusInternalServerError)
					return
				}
			}
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/dependency")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.FlushInterval = -1
			proxy.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/upload":
			if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
				http.Error(w, "fixture streaming unavailable", http.StatusInternalServerError)
				return
			}
			if _, err := io.WriteString(w, "ready\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			if _, err := io.Copy(w, r.Body); err != nil {
				return
			}
			return
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		case "/socket":
			connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			for {
				kind, payload, err := connection.ReadMessage()
				if err != nil {
					return
				}
				if err := connection.WriteMessage(kind, payload); err != nil {
					return
				}
			}
		}
		if r.Header.Get("Authorization") == "Bearer platform-token" {
			http.Error(w, "unexpected management credential", http.StatusInternalServerError)
			return
		}
		if _, err := io.WriteString(w, "managed application response"); err != nil {
			return
		}
	})}
	if err = server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}

func applicationManagerFixture(t *testing.T, serverURL string) (*Daemon, protocol.ApplicationControlCommand) {
	t.Helper()
	const daemonID = "66666666-6666-4666-8666-666666666666"
	const workspaceID = "22222222-2222-4222-8222-222222222222"
	const runtimeID = "33333333-3333-4333-8333-333333333333"
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := protocol.DefaultApplicationConfig()
	config.ResourceID = "55555555-5555-4555-8555-555555555555"
	config.Command = []string{executable, "-test.run=^TestApplicationDaemonServiceFixture$"}
	config.Environment = map[string]string{"APPLICATION_DAEMON_FIXTURE": "1", "APPLICATION_START_FILE": filepath.Join(source, "starts")}
	config.Port = port
	config.Health.Kind = "http"
	config.Health.Path = "/"
	config.Health.TimeoutSeconds = 8
	config.Health.IntervalSeconds = 1
	resource, err := json.Marshal(localDirectoryRef{LocalPath: source, DaemonID: daemonID, ExecutionMode: "in_place"})
	if err != nil {
		t.Fatal(err)
	}
	command := protocol.ApplicationControlCommand{InstanceID: "11111111-1111-4111-8111-111111111111", ApplicationID: "44444444-4444-4444-8444-444444444444", WorkspaceID: workspaceID, RuntimeID: runtimeID, Generation: 1, Revision: 1, Action: "start", Config: config, ResourceType: "local_directory", ResourceRef: resource, Dependencies: []protocol.ApplicationInstanceDependency{}}
	d := &Daemon{cfg: Config{DaemonID: daemonID, WorkspacesRoot: root}, client: NewClient(serverURL), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), runtimeIndex: map[string]Runtime{runtimeID: {ID: runtimeID, Status: "online"}}, workspaces: map[string]*workspaceState{workspaceID: {workspaceID: workspaceID, runtimeIDs: []string{runtimeID}}}, applicationWake: make(chan struct{}, 1)}
	d.applicationHostLauncher = func(path string) (*exec.Cmd, error) {
		cmd := exec.Command(executable, "-test.run=^TestApplicationDaemonHostFixture$", "--", applicationhost.Entrypoint, path)
		detachApplicationHost(cmd)
		return cmd, nil
	}
	d.applicationServerCapabilities.Store(runtimeID, true)
	return d, command
}

func waitApplicationManager(t *testing.T, predicate func() bool) {
	t.Helper()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("application lifecycle did not converge")
		case <-ticker.C:
		}
	}
}

func TestApplicationDaemonManagerStartsRecoversAndStopsRealService(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	observation, err := d.executeApplication(ctx, command)
	if err != nil || observation.ProcessState != "running" || observation.HealthState != "healthy" {
		t.Fatalf("start: %+v %v", observation, err)
	}
	t.Cleanup(func() {
		stop := command
		stop.Action = "stop"
		stop.Generation = 2
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := d.executeApplication(cleanupCtx, stop); err != nil {
			t.Error(err)
		}
	})
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(command.Config.Port))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(raw) != "managed application response" {
		t.Fatalf("real response=%s err=%v", raw, err)
	}
	if referenced, err := d.applicationReferencesDirectory(d.cfg.WorkspacesRoot); err != nil || !referenced {
		t.Fatalf("live source was not protected: %v %v", referenced, err)
	}
	if _, err = d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("duplicate delivery restarted service: %s %v", starts, err)
	}
	_, record, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	client, err := applicationhost.NewClient(record)
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(ctx)
	if err != nil || status.Observation.InstanceID != command.InstanceID {
		t.Fatal("local ownership recovery failed")
	}
	stop := command
	stop.Action = "stop"
	stop.Generation = 2
	if stopped, err := d.executeApplication(ctx, stop); err != nil || stopped.ProcessState != "stopped" {
		t.Fatalf("stop=%+v %v", stopped, err)
	}
	waitApplicationManager(t, func() bool { _, err := client.Status(ctx); return err != nil })
	page, err := d.readApplicationLogs(ctx, stop, "", 65536)
	if err != nil || !strings.Contains(page.Text, "application service started") {
		t.Fatalf("stopped host lost its local logs: %+v %v", page, err)
	}
	if referenced, err := d.applicationReferencesDirectory(d.cfg.WorkspacesRoot); err != nil || referenced {
		t.Fatalf("stopped application retained live reference: %v %v", referenced, err)
	}
}

func TestApplicationDaemonLoopReportsAndSurvivesControllerRestart(t *testing.T) {
	var mu sync.Mutex
	var command protocol.ApplicationControlCommand
	var pending []protocol.ApplicationClaim
	var results []protocol.ApplicationStepResult
	var observations []protocol.ApplicationObservation
	desired := "running"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/sync"):
			if err := json.NewEncoder(w).Encode([]protocol.ApplicationRuntimeInstance{{DesiredState: desired, Command: command}}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/claim"):
			claims := pending
			pending = nil
			if claims == nil {
				claims = []protocol.ApplicationClaim{}
			}
			if err := json.NewEncoder(w).Encode(claims); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/observe"):
			var input struct {
				Observation protocol.ApplicationObservation `json:"observation"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			observations = append(observations, input.Observation)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/result"):
			var input struct {
				Result protocol.ApplicationStepResult `json:"result"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			results = append(results, input.Result)
			if _, err := fmt.Fprint(w, `{"operation_id":"fixture"}`); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/lease"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d, initial := applicationManagerFixture(t, server.URL)
	command = initial
	claim := protocol.ApplicationClaim{StepID: "77777777-7777-4777-8777-777777777777", OperationID: "88888888-8888-4888-8888-888888888888", ClaimToken: "99999999-9999-4999-8999-999999999999", Command: command}
	pending = []protocol.ApplicationClaim{claim}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.applicationLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		stop := initial
		stop.Action = "stop"
		stop.Generation = 2
		if _, err := d.executeApplication(stopCtx, stop); err != nil {
			t.Error(err)
		}
	})
	waitApplicationManager(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(results) > 0 })
	mu.Lock()
	if results[0].State != "completed" || results[0].Observation.HealthState != "healthy" {
		t.Fatalf("actual operation result: %+v", results[0])
	}
	mu.Unlock()
	cancel()
	<-done
	if err := applicationhost.CheckHealth(context.Background(), initial.Config); err != nil {
		t.Fatalf("controller context stopped the service: %v", err)
	}
	recovered := &Daemon{cfg: d.cfg, client: NewClient(server.URL), logger: d.logger, runtimeIndex: d.runtimeIndex, workspaces: d.workspaces, applicationWake: make(chan struct{}, 1), applicationHostLauncher: d.applicationHostLauncher}
	recovered.applicationServerCapabilities.Store(initial.RuntimeID, true)
	mu.Lock()
	observations = nil
	mu.Unlock()
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	recoveryDone := make(chan struct{})
	go func() { defer close(recoveryDone); recovered.applicationLoop(recoveryCtx) }()
	t.Cleanup(func() { recoveryCancel(); <-recoveryDone })
	waitApplicationManager(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, observed := range observations {
			if observed.ProcessState == "running" && observed.HealthState == "healthy" {
				return true
			}
		}
		return false
	})
	starts, err := os.ReadFile(initial.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("controller recovery duplicated the process: %s %v", starts, err)
	}
	mu.Lock()
	desired = "stopped"
	command.Action = "stop"
	command.Generation = 2
	mu.Unlock()
	recovered.wakeApplications()
	waitApplicationManager(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, observed := range observations {
			if observed.Generation == 2 && observed.ProcessState == "stopped" {
				return true
			}
		}
		return false
	})
}

func TestApplicationSourceUsesScopedStableCheckout(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	cache := repocache.New(filepath.Join(d.cfg.WorkspacesRoot, "cache"), d.logger)
	d.repoCache = cache
	var local localDirectoryRef
	if err := json.Unmarshal(command.ResourceRef, &local); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(local.LocalPath, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	command.Config.WorkDir = "nested"
	root, workDir, _, _, err := d.applicationSource(context.Background(), command)
	if err != nil || workDir != filepath.Join(root, "nested") {
		t.Fatalf("nested source=%s %s %v", root, workDir, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(local.LocalPath, "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	command.Config.WorkDir = "escape"
	if _, _, _, _, err = d.applicationSource(context.Background(), command); err == nil {
		t.Fatal("symlink escaped the application resource")
	}
	local.DaemonID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	raw, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	command.ResourceRef = raw
	command.Config.WorkDir = ""
	if _, _, _, _, err = d.applicationSource(context.Background(), command); err == nil {
		t.Fatal("foreign machine resource was accepted")
	}
}
