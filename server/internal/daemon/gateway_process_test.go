package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time proof the seam is satisfied by both owners.
var (
	_ mcpBroker = (*builtinMCPBroker)(nil)
	_ mcpBroker = (*gatewayProcessClient)(nil)
)

func TestGatewayProcessCallbackRoutesToControlHandler(t *testing.T) {
	c, err := newGatewayProcessCallback()
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer c.close()

	const want = "control-handler-ran"
	var calls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-Control-Seen", r.Header.Get("X-Agent-Marker"))
		w.Write([]byte(want))
	})
	callbackURL, unregister := c.register(handler)
	if callbackURL == "" {
		t.Fatal("expected a callback URL")
	}

	// A registered route dispatches to the control-owned handler.
	req, _ := http.NewRequest(http.MethodPost, callbackURL, nil)
	req.Header.Set("X-Agent-Marker", "agent")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != want {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
	if res.Header.Get("X-Control-Seen") != "agent" {
		t.Fatalf("handler did not see agent marker: %q", res.Header.Get("X-Control-Seen"))
	}
	if calls != 1 {
		t.Fatalf("handler called %d times", calls)
	}

	// An exact unregister refuses the route afterwards (revoked, not executed).
	unregister()
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request after unregister: %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after revoke, got %d", res2.StatusCode)
	}
}

func TestGatewayForwarderReachesControlCallback(t *testing.T) {
	// The gateway forwarder must reach control's callback and return its body.
	var seenMarker string
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMarker = r.Header.Get("X-Agent-Marker")
		w.Header().Set("X-From-Control", "yes")
		w.Write([]byte("forwarded"))
	}))
	defer control.Close()

	f := gatewayForwarder{callbackURL: control.URL + "/cb/abc"}
	req, _ := http.NewRequest(http.MethodPost, "http://gateway.local/task/mcp", nil)
	req.Header.Set("X-Agent-Marker", "agent")
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, req)
	if rec.Body.String() != "forwarded" {
		t.Fatalf("forward body = %q", rec.Body.String())
	}
	if seenMarker != "agent" {
		t.Fatalf("control did not receive agent marker: %q", seenMarker)
	}
}

func TestGatewayForwarderRejectsNonLoopback(t *testing.T) {
	f := gatewayForwarder{callbackURL: "http://evil.example/cb/abc"}
	req, _ := http.NewRequest(http.MethodGet, "http://gateway.local/x", nil)
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for non-loopback target, got %d", rec.Code)
	}
}

// The legacy in-process broker still satisfies the seam and routes directly.
func TestInProcessBrokerSatisfiesSeam(t *testing.T) {
	broker, err := startBuiltinMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer broker.close()
	var _ mcpBroker = broker

	var ran bool
	endpoint, unregister := broker.register("/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.Write([]byte("ok"))
	}))
	if endpoint == "" {
		t.Fatal("expected an endpoint")
	}
	res, err := http.DefaultClient.Get(endpoint)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !ran {
		t.Fatal("handler did not run")
	}
	unregister()
	if !broker.ready() {
		t.Fatal("broker should remain ready after a single route unregister")
	}
}

// TestValidateGatewayForwardTarget proves the callback target security boundary
// that a gateway/worker forwarder must enforce before it can forward an agent
// request to the control-owned callback: only a bare 127.0.0.1 http origin is
// accepted. A non-loopback host, a non-http scheme, a portless origin, an
// origin carrying credentials, or an origin carrying a fragment must be
// rejected, because the forward proxy is the boundary between the agent's
// public route and the control-owned callback.
func TestValidateGatewayForwardTarget(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid loopback with port", "http://127.0.0.1:5432/cb", false},
		{"valid loopback path only", "http://127.0.0.1:80", false},
		{"non-loopback host rejected", "http://evil.example:80/cb", true},
		{"localhost is not 127.0.0.1", "http://localhost:5432/cb", true},
		{"https scheme rejected", "https://127.0.0.1:5432/cb", true},
		{"missing port rejected", "http://127.0.0.1/cb", true},
		{"credentials in target rejected", "http://user:pass@127.0.0.1:5432/cb", true},
		{"fragment in target rejected", "http://127.0.0.1:5432/cb#frag", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateGatewayForwardTarget(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected a rejection for %q, got nil", c.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected rejection for %q: %v", c.raw, err)
			}
		})
	}
}
