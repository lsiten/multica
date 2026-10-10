package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuiltinMCPBrokerKeepsListenerAndScopesRoutes(t *testing.T) {
	broker, err := startBuiltinMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("startBuiltinMCPBroker: %v", err)
	}
	t.Cleanup(broker.close)

	firstURL, unregisterFirst := broker.register("/first", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if firstURL == "" || !broker.ready() {
		t.Fatalf("broker not ready: url=%q", firstURL)
	}
	request := httptest.NewRequest(http.MethodPost, firstURL, nil)
	recorder := httptest.NewRecorder()
	broker.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("first route status = %d", recorder.Code)
	}

	unregisterFirst()
	recorder = httptest.NewRecorder()
	broker.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unregistered route status = %d", recorder.Code)
	}

	broker.close()
	if broker.ready() {
		t.Fatal("closed broker remained ready")
	}
}

func TestBuiltinMCPBrokerReadinessExposesBothBuiltIns(t *testing.T) {
	broker, err := startBuiltinMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("startBuiltinMCPBroker: %v", err)
	}
	defer broker.close()
	d := &Daemon{builtinMCP: broker}
	statuses := d.mcpReadinessSnapshot()
	seen := map[string]MCPReadinessSnapshot{}
	for _, status := range statuses {
		if status.Scope == "daemon" {
			seen[status.Name] = status
		}
	}
	for _, name := range []string{llm2jevMCPName, identityActionsMCPName} {
		status, ok := seen[name]
		if !ok || !status.Ready || status.State != MCPReadinessBrokerReady || status.Reason != "broker_listening" {
			t.Fatalf("missing ready built-in %q: %+v", name, statuses)
		}
	}
}

func TestBuiltinMCPBrokerRegistrationGenerationIsExact(t *testing.T) {
	broker, err := startBuiltinMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("startBuiltinMCPBroker: %v", err)
	}
	defer broker.close()

	// Two registrations on the same path: the second supersedes the first.
	unregisterV1 := func() {}
	{
		_, u := broker.register("/dup", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("v1"))
		}))
		unregisterV1 = u
	}
	url := ""
	unregisterV2 := func() {}
	{
		u, unregister := broker.register("/dup", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("v2"))
		}))
		url, unregisterV2 = u, unregister
	}
	if url == "" {
		t.Fatal("re-registered route returned no url")
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)

	// A stale revoke (from the superseded registration) must not delete the
	// route the newer registration owns.
	unregisterV1()
	recorder := httptest.NewRecorder()
	broker.ServeHTTP(recorder, req)
	if body := recorder.Body.String(); body != "v2" {
		t.Fatalf("stale revoke removed the current route: got %q, want v2", body)
	}

	// The current revoke removes the route.
	unregisterV2()
	recorder = httptest.NewRecorder()
	broker.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("current revoke did not remove the route: status = %d", recorder.Code)
	}
}
