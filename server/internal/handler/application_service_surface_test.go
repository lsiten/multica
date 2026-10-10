package handler

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func applicationGrantRedacted(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, entry := range value {
			if (key == "token" || key == "claim_token" || key == "grant") && entry != "" {
				value[key] = "REDACTED"
			} else {
				value[key] = applicationGrantRedacted(entry)
			}
		}
	case []any:
		for index, entry := range value {
			value[index] = applicationGrantRedacted(entry)
		}
	}
	return value
}

func recordApplicationGrantSurface(t *testing.T, name string, evidence any) {
	t.Helper()
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if directory := os.Getenv("MULTICA_APPLICATION_GRANT_EVIDENCE_DIR"); directory != "" {
		if err = os.WriteFile(filepath.Join(directory, name), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplicationServiceGrantLiteralHTTPProof(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	_, operation := f.app(t)
	server := f.server(t)
	type exchange struct {
		Method, Path string
		Status       int
		Response     any
	}
	var exchanges []exchange
	request := func(method, path, token string, body any, want int, out any) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		config := "url = " + strconv.Quote(server.URL+path) + "\nrequest = " + strconv.Quote(method) + "\nheader = " + strconv.Quote("Authorization: Bearer "+token) + "\nheader = \"Content-Type: application/json\"\n"
		if method != "GET" {
			config += "data = " + strconv.Quote(string(payload)) + "\n"
		}
		command := exec.Command("curl", "--silent", "--show-error", "--config", "-", "--write-out", "\n%{http_code}")
		command.Stdin = strings.NewReader(config)
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("curl request failed: %v", err)
		}
		index := strings.LastIndexByte(string(raw), '\n')
		if index < 0 {
			t.Fatal("missing curl status")
		}
		status, err := strconv.Atoi(string(raw[index+1:]))
		if err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("curl %s %s status=%d want=%d", method, path, status, want)
		}
		response := raw[:index]
		if out != nil {
			if err = json.Unmarshal(response, out); err != nil {
				t.Fatal(err)
			}
		}
		var redacted any
		if len(response) > 0 {
			if err = json.Unmarshal(response, &redacted); err != nil {
				t.Fatal(err)
			}
			redacted = applicationGrantRedacted(redacted)
		}
		exchanges = append(exchanges, exchange{method, path, status, redacted})
	}
	var before string
	f.fx.QueryRow(t, "SELECT state FROM application_operation_step WHERE operation_id=$1", operation.ID).Scan(&before)
	var state protocol.ApplicationServiceGrantState
	request("GET", f.path("/application-service-grants"), f.control, nil, 200, &state)
	var grant protocol.ApplicationServiceGrantResponse
	request("POST", f.path("/application-service-grants"), f.control, f.grantInput(state.Generation), 200, &grant)
	request("POST", f.path("/applications/sync"), grant.Token, f.body(), 200, nil)
	var claims []protocol.ApplicationClaim
	request("POST", f.path("/applications/claim"), grant.Token, f.body(), 200, &claims)
	if len(claims) != 1 {
		t.Fatal("curl did not claim exact operation")
	}
	claim := claims[0]
	request("POST", f.path("/applications/steps/"+claim.StepID+"/lease"), grant.Token, map[string]string{"daemon_id": f.daemon, "claim_token": claim.ClaimToken}, 204, nil)
	observation := protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}
	request("POST", f.path("/applications/observe"), grant.Token, map[string]any{"daemon_id": f.daemon, "observation": observation}, 204, nil)
	result := protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "completed", Observation: observation}
	for range 2 {
		request("POST", f.path("/applications/steps/"+claim.StepID+"/result"), grant.Token, map[string]any{"daemon_id": f.daemon, "result": result}, 200, nil)
	}
	request("POST", f.path("/application-service-grants/revoke"), f.control, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: grant.ServiceInstanceID, Generation: grant.Generation}, 204, nil)
	request("POST", f.path("/applications/sync"), grant.Token, f.body(), 401, nil)
	var after string
	f.fx.QueryRow(t, "SELECT state FROM application_operation_step WHERE id=$1", claim.StepID).Scan(&after)
	var generation int64
	var revoked bool
	f.fx.QueryRow(t, "SELECT generation,revoked FROM application_service_authority WHERE runtime_id=$1", f.runtime).Scan(&generation, &revoked)
	if before != "queued" || after != "completed" || generation != 2 || !revoked {
		t.Fatalf("HTTP DB readback: %s -> %s generation=%d revoked=%t", before, after, generation, revoked)
	}
	recordApplicationGrantSurface(t, "http-proof.json", map[string]any{"invocation": "curl --silent --show-error --config - --write-out '\\n%{http_code}' (credentials and JSON on private stdin)", "listener": server.URL, "runtime_id": f.runtime, "service_instance_id": grant.ServiceInstanceID, "operation_id": operation.ID, "step_id": claim.StepID, "before": before, "after": after, "authority_generation": generation, "revoked": revoked, "exchanges": exchanges, "cleanup": "owned httptest listener, sockets, and fixture rows cleaned by test cleanup"})
	t.Logf("literal HTTP proof: %d requests, operation=%s %s -> %s; grant and claim tokens redacted", len(exchanges), operation.ID, before, after)
}

func TestApplicationServiceGrantRealBackendRotationDoesNotReplayPOST(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	server := f.server(t)
	var requests atomic.Int64
	blocked := make(chan struct{})
	cancelled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "side-effect-once" || r.Header.Get("Authorization") != "" {
			t.Errorf("backend request changed or received management credential")
		}
		if r.URL.Path == "/blocked" {
			close(blocked)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("backend accepted once"))
	}))
	t.Cleanup(backend.Close)
	tunnel := f.tunnel(t, server, f.grant.Token, f.grant.Token)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTimeout("tcp", target.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		applicationgateway.Bridge(applicationgateway.NewStream(tunnel.data, nil), local)
	}()
	t.Cleanup(func() { tunnel.data.Close(); local.Close(); <-finished })
	first, err := http.NewRequest("POST", backend.URL+"/first", strings.NewReader("side-effect-once"))
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Write(tunnel.stream); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(tunnel.stream)
	response, err := http.ReadResponse(reader, first)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "backend accepted once" {
		t.Fatalf("real backend response status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	second, err := http.NewRequest("POST", backend.URL+"/blocked", strings.NewReader("side-effect-once"))
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Write(tunnel.stream); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("second POST did not reach real backend")
	}
	var renewed protocol.ApplicationServiceGrantResponse
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), f.control, f.grantInput(1))).Want(200).JSON(&renewed)
	select {
	case <-cancelled:
	case <-time.After(4 * time.Second):
		t.Fatal("grant rotation did not cancel in-flight backend request")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("data bridge survived revocation")
	}
	if _, err = http.ReadResponse(reader, second); err == nil {
		t.Fatal("unconfirmed POST was reported as successful")
	}
	reconnected := f.dial(t, server, "control", renewed.Token)
	reconnected.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	var replay protocol.ApplicationTunnelRequest
	if err = reconnected.ReadJSON(&replay); err == nil {
		t.Fatal("rotation replayed an arbitrary application request")
	}
	reconnected.Close()
	if requests.Load() != 2 {
		t.Fatalf("backend POST count=%d", requests.Load())
	}
	recordApplicationGrantSurface(t, "websocket-backend-proof.json", map[string]any{"runtime_id": f.runtime, "gateway_listener": server.URL, "backend_listener": backend.URL, "backend_posts": requests.Load(), "first_response_status": response.StatusCode, "inflight_post_cancelled": true, "arbitrary_post_replayed": false, "replacement_generation": renewed.Generation, "reconnected_under_new_grant": true, "one_use_stream_handshake": true, "management_authorization_forwarded": false, "cleanup": "both listeners, both websocket directions and bridge workers joined through test cleanup"})
}
