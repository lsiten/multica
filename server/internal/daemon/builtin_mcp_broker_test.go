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
