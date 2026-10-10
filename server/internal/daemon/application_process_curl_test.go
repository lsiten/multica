package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationProcessCurlSurface(t *testing.T) {
	evidence := os.Getenv("APPLICATION_PROCESS_CURL_EVIDENCE_DIR")
	if evidence == "" {
		t.Skip("manual curl evidence directory not configured")
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	d, child, backend := newApplicationProcessFixture(t, func(d *Daemon, _ *protocol.ApplicationControlCommand) {
		if executable := os.Getenv("APPLICATION_PROCESS_EXECUTABLE"); executable != "" {
			d.cfg.NativeHostExecutable = executable
			d.cfg.NativeHostBuild = "dev/unknown"
		}
	})
	waitApplicationManager(t, func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.results) > 0 })
	backend.mu.Lock()
	command := backend.command
	backend.mu.Unlock()
	inventory, err := child.inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record, err := runtimeproc.ReadRecord(child.bootstrap.Root, child.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	credentialFile := filepath.Join(directory, "curl-private.conf")
	if err = os.WriteFile(credentialFile, []byte("header = \"Authorization: Bearer "+record.Token+"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var transcript bytes.Buffer
	curl := func(name, url string, input any, authenticated bool) []byte {
		t.Helper()
		args := []string{"--silent", "--show-error", "-i", "--max-time", "15"}
		if authenticated {
			args = append(args, "--config", credentialFile)
		}
		if input != nil {
			body, _ := json.Marshal(input)
			path := filepath.Join(directory, "request.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--header", "Content-Type: application/json", "--data-binary", "@"+path)
		}
		args = append(args, url)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "curl", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("curl %s: %v %s", name, err, output)
		}
		transcript.WriteString("scenario: " + name + "\ninvocation: curl -i --max-time 15 [private credential/body files] " + url + "\n")
		transcript.Write(output)
		transcript.WriteString("\n")
		return output
	}
	status, err := child.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request, err := child.process.Client.Request("application.inventory", runtimeproc.Fence{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output := curl("unauthenticated", record.Address+"/rpc", request, false); !bytes.Contains(output, []byte("401")) {
		t.Fatal("unauthenticated curl accepted")
	}
	if output := curl("authenticated inventory", record.Address+"/rpc", request, true); !bytes.Contains(output, []byte("200 OK")) || !bytes.Contains(output, []byte(`"running"`)) {
		t.Fatal("inventory curl failed")
	}
	mutation, err := child.process.Client.Request("application.wake", status.Fence, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output := curl("mutation", record.Address+"/rpc", mutation, true); !bytes.Contains(output, []byte("200 OK")) {
		t.Fatal("mutation curl failed")
	}
	mutation.Payload = json.RawMessage(`{"different":true}`)
	if output := curl("same ID changed payload", record.Address+"/rpc", mutation, true); !bytes.Contains(output, []byte("request_conflict")) {
		t.Fatal("changed payload did not conflict")
	}
	waitApplicationManager(t, func() bool { return backend.hub.Connected(command.RuntimeID) })
	descriptor := protocol.ApplicationTunnelRequest{EndpointID: "curl-fixture", RuntimeID: command.RuntimeID, WorkspaceID: command.WorkspaceID, InstanceID: command.InstanceID, Generation: command.Generation, Kind: "service", Port: command.Config.Port}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return backend.hub.Open(ctx, descriptor)
	}}
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.Out.URL.Scheme = "http"; r.Out.URL.Host = "service.local" }, Transport: transport, FlushInterval: -1})
	defer proxy.Close()
	if output := curl("backend data plane to independent app", proxy.URL+"/", nil, false); !bytes.Contains(output, []byte("managed application response")) {
		t.Fatal("direct application curl failed")
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return backend.hub.Open(ctx, descriptor)
	}}
	socket, response, err := dialer.DialContext(t.Context(), "ws://service.local/socket", nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = socket.WriteMessage(websocket.TextMessage, []byte("direct-child-websocket")); err != nil {
		t.Fatal(err)
	}
	_, message, err := socket.ReadMessage()
	socket.Close()
	if err != nil || string(message) != "direct-child-websocket" {
		t.Fatal("application websocket echo failed")
	}
	transcript.WriteString("websocket observable: direct-child-websocket echoed through child data socket; parent daemon has no tunnel loop\n")
	hostPathForPID, _, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	processList, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostPID := 0
	for _, line := range strings.Split(string(processList), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[3] == applicationhost.Entrypoint && fields[4] == hostPathForPID {
			hostPID, _ = strconv.Atoi(fields[0])
		}
	}
	if hostPID < 1 || hostPID == inventory.PID || hostPID == os.Getpid() {
		t.Fatal("actual independent host PID unavailable")
	}
	status, err = child.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stop, err := child.process.Client.Request("stop", status.Fence, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop.Deadline = time.Now().Add(30 * time.Second)
	if output := curl("graceful stopped", record.Address+"/rpc", stop, true); !bytes.Contains(output, []byte(`"state":"stopped"`)) {
		t.Fatal("stop curl failed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err = child.process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	child.source.close()
	child.closeOnce.Do(func() {})
	hostPath, host, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	if err = applicationhost.WaitStopped(ctx, hostPath, host.HostID); err != nil {
		t.Fatal(err)
	}
	for id, grant := range child.grantSnapshot() {
		if err = d.client.RevokeApplicationServiceGrant(ctx, id, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: grant.ServiceInstanceID, Generation: grant.Generation}); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{record.Address, child.source.address} {
		connection, err := net.DialTimeout("tcp", strings.TrimPrefix(origin, "http://"), 100*time.Millisecond)
		if err == nil {
			connection.Close()
			t.Fatal("fixture listener leaked")
		}
	}
	backend.mu.Lock()
	control, data := backend.controlConnections, backend.dataConnections
	backend.mu.Unlock()
	cleanup := map[string]any{"controller_pid": os.Getpid(), "manager_pid": inventory.PID, "host_id": host.HostID, "host_pid": hostPID, "executable": d.cfg.NativeHostExecutable, "manager_waited": true, "host_stopped_and_kernel_lock_released": true, "source_and_control_listeners_closed": true, "direct_control_connections": control, "direct_data_connections": data, "real_accounts": false, "real_agents": false, "real_native_prompts": false}
	raw, _ := json.MarshalIndent(cleanup, "", "  ")
	if err = os.WriteFile(filepath.Join(evidence, "curl-cleanup.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(evidence, "curl-surface.log"), transcript.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("curl -i real manager PID%d; direct HTTP and websocket; stopped host lock; artifact %s", inventory.PID, filepath.Join(evidence, "curl-surface.log"))
}
