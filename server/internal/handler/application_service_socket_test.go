package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (f applicationServiceGrantFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(f.router)
	t.Cleanup(func() { f.handler.ApplicationGateway.Close(); server.Close() })
	return server
}

func (f applicationServiceGrantFixture) dial(t *testing.T, server *httptest.Server, kind, token string) *websocket.Conn {
	t.Helper()
	address := "ws" + strings.TrimPrefix(server.URL, "http") + f.path("/applications/tunnel/"+kind) + "?daemon_id=" + f.daemon
	connection, response, err := websocket.DefaultDialer.Dial(address, http.Header{"Authorization": []string{"Bearer " + token}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		code := 0
		if response != nil {
			code = response.StatusCode
		}
		t.Fatalf("application %s socket status=%d err=%v", kind, code, err)
	}
	t.Cleanup(func() { connection.Close() })
	return connection
}

type applicationGrantTunnel struct {
	control, data *websocket.Conn
	stream        net.Conn
	controlClosed <-chan struct{}
	request       protocol.ApplicationTunnelRequest
}

func (f applicationServiceGrantFixture) tunnel(t *testing.T, server *httptest.Server, controlToken, dataToken string) applicationGrantTunnel {
	t.Helper()
	control := f.dial(t, server, "control", controlToken)
	requests := make(chan protocol.ApplicationTunnelRequest, 2)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			var request protocol.ApplicationTunnelRequest
			if err := control.ReadJSON(&request); err != nil {
				return
			}
			requests <- request
		}
	}()
	t.Cleanup(func() { control.Close(); <-closed })
	deadline := time.Now().Add(3 * time.Second)
	for !f.handler.ApplicationGateway.Connected(f.runtime) {
		if time.Now().After(deadline) {
			t.Fatal("control socket not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	opened := make(chan net.Conn, 1)
	failed := make(chan error, 1)
	go func() {
		stream, err := f.handler.ApplicationGateway.Open(ctx, protocol.ApplicationTunnelRequest{RuntimeID: f.runtime, WorkspaceID: f.workspace, InstanceID: uuid.NewString(), EndpointID: uuid.NewString(), Generation: 1, Kind: "service", Port: 4100})
		if err != nil {
			failed <- err
			return
		}
		opened <- stream
	}()
	var request protocol.ApplicationTunnelRequest
	select {
	case request = <-requests:
	case err := <-failed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("gateway did not request one-use stream")
	}
	data := f.dial(t, server, "data", dataToken)
	if err := data.WriteJSON(map[string]string{"stream_id": request.StreamID, "token": request.Token}); err != nil {
		t.Fatal(err)
	}
	var stream net.Conn
	select {
	case stream = <-opened:
	case err := <-failed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("data handshake did not attach")
	}
	t.Cleanup(func() { stream.Close() })
	if _, err := stream.Write([]byte("actual application stream")); err != nil {
		t.Fatal(err)
	}
	data.SetReadDeadline(time.Now().Add(3 * time.Second))
	kind, body, err := data.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(body) != "actual application stream" {
		t.Fatalf("attached stream payload=%q kind=%d err=%v", body, kind, err)
	}
	data.SetReadDeadline(time.Time{})
	return applicationGrantTunnel{control: control, data: data, stream: stream, controlClosed: closed, request: request}
}

func waitApplicationDataClosed(t *testing.T, connection *websocket.Conn) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, err := connection.ReadMessage()
	if err == nil {
		t.Fatal("revoked data stream remained readable")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("socket stayed open until deadline: %v", err)
	}
}

func TestApplicationServiceGrantClosesLiveControlAndData(t *testing.T) {
	for _, scenario := range []string{"expiry", "revoke", "same_instance_rotation", "replacement", "operation_reduction", "membership", "runtime_deleted", "runtime_daemon_replaced"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApplicationServiceGrantFixture(t)
			if scenario == "expiry" {
				f.fx.Exec(t, "UPDATE application_service_grant SET expires_at=clock_timestamp()+interval '1500 milliseconds' WHERE token_hash=$1", auth.HashToken(f.grant.Token))
			}
			server := f.server(t)
			tunnel := f.tunnel(t, server, f.grant.Token, f.grant.Token)
			var renewed protocol.ApplicationServiceGrantResponse
			switch scenario {
			case "revoke":
				testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants/revoke"), f.control, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: f.instance, Generation: 1})).Want(204)
			case "same_instance_rotation", "replacement", "operation_reduction":
				input := f.grantInput(1)
				if scenario == "replacement" {
					input.ServiceInstanceID = uuid.NewString()
				}
				if scenario == "operation_reduction" {
					input.Operations = []string{"sync"}
				}
				testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), f.control, input)).Want(200).JSON(&renewed)
			case "membership":
				f.fx.Exec(t, "DELETE FROM member WHERE id=$1", f.member)
			case "runtime_deleted":
				f.fx.Exec(t, "DELETE FROM agent_runtime WHERE id=$1", f.runtime)
			case "runtime_daemon_replaced":
				f.fx.Exec(t, "UPDATE agent_runtime SET daemon_id=$2 WHERE id=$1", f.runtime, uuid.NewString())
			}
			waitApplicationDataClosed(t, tunnel.data)
			select {
			case <-tunnel.controlClosed:
			case <-time.After(4 * time.Second):
				t.Fatal("revoked control socket remained open")
			}
			testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), f.grant.Token, f.body())).Want(401)
			if renewed.Token != "" && scenario != "operation_reduction" {
				connection := f.dial(t, server, "control", renewed.Token)
				if err := connection.WriteMessage(websocket.TextMessage, []byte(`{}`)); err != nil {
					t.Fatal("new valid grant could not reconnect")
				}
				connection.Close()
			}
			if scenario == "operation_reduction" {
				testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/applications/sync"), renewed.Token, f.body())).Want(200)
				address := "ws" + strings.TrimPrefix(server.URL, "http") + f.path("/applications/tunnel/control") + "?daemon_id=" + f.daemon
				connection, response, err := websocket.DefaultDialer.Dial(address, http.Header{"Authorization": []string{"Bearer " + renewed.Token}})
				if connection != nil {
					connection.Close()
					t.Fatal("reduced grant opened control socket")
				}
				if err == nil || response == nil || response.StatusCode != 403 {
					t.Fatal("reduced grant did not reject control before upgrade")
				}
				response.Body.Close()
			}
		})
	}
}

func TestApplicationServiceGrantDataRevocationPreservesLegacyControl(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	server := f.server(t)
	tunnel := f.tunnel(t, server, f.control, f.grant.Token)
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants/revoke"), f.control, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: f.instance, Generation: 1})).Want(204)
	waitApplicationDataClosed(t, tunnel.data)
	if !f.handler.ApplicationGateway.Connected(f.runtime) {
		t.Fatal("scoped grant revocation disconnected legacy control")
	}
	select {
	case <-tunnel.controlClosed:
		t.Fatal("legacy control inherited grant expiry")
	default:
	}
}

func TestApplicationServiceGrantDoesNotBypassOneUseStreamToken(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	server := f.server(t)
	tunnel := f.tunnel(t, server, f.grant.Token, f.grant.Token)
	duplicate := f.dial(t, server, "data", f.grant.Token)
	if err := duplicate.WriteJSON(map[string]string{"stream_id": tunnel.request.StreamID, "token": tunnel.request.Token}); err != nil {
		t.Fatal(err)
	}
	waitApplicationDataClosed(t, duplicate)
	forged := f.dial(t, server, "data", f.grant.Token)
	if err := forged.WriteJSON(map[string]string{"stream_id": strings.Repeat("b", 64), "token": strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	waitApplicationDataClosed(t, forged)
	if _, err := tunnel.stream.Write([]byte("original survives")); err != nil {
		t.Fatal(err)
	}
	tunnel.data.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := tunnel.data.ReadMessage()
	if err != nil || string(payload) != "original survives" {
		t.Fatal("invalid duplicate affected authorized stream")
	}
}

func TestApplicationServiceGrantScopeIsCheckedBeforeUpgrade(t *testing.T) {
	f := newApplicationServiceGrantFixture(t)
	server := f.server(t)
	input := f.grantInput(1)
	input.Operations = []string{"sync"}
	var restricted protocol.ApplicationServiceGrantResponse
	testutil.Call(t, f.router.ServeHTTP, applicationServiceRequest("POST", f.path("/application-service-grants"), f.control, input)).Want(200).JSON(&restricted)
	for _, tc := range []struct {
		token, runtime, daemon string
		want                   int
	}{
		{restricted.Token, f.runtime, f.daemon, 403},
		{f.grant.Token, f.runtime, f.daemon, 401},
		{restricted.Token, uuid.NewString(), f.daemon, 403},
	} {
		address := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/daemon/runtimes/" + tc.runtime + "/applications/tunnel/control?daemon_id=" + tc.daemon
		connection, response, err := websocket.DefaultDialer.Dial(address, http.Header{"Authorization": []string{"Bearer " + tc.token}})
		if connection != nil {
			connection.Close()
			t.Fatal("out-of-scope socket upgraded")
		}
		if response == nil || response.StatusCode != tc.want || err == nil {
			t.Fatalf("pre-upgrade scope status=%v err=%v", response, err)
		}
		response.Body.Close()
	}
}
