package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMCPReadinessSharesDuplicateEndpointUntilLastCleanup(t *testing.T) {
	var initializeCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{"tools": []any{}}
		if request.Method == "initialize" {
			initializeCalls.Add(1)
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}
		}
		writeLLM2JevMCPResult(w, request.ID, result)
	}))
	defer server.Close()
	d := &Daemon{}
	first := registerTaskManagedMCPReadiness(d, "workspace", llm2jevMCPName, server.URL, "task", true)
	defer first()
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{llm2jevMCPName: map[string]any{"url": server.URL, "type": "http"}}})
	if err != nil {
		t.Fatal(err)
	}
	second := registerTaskMCPReadinessFromConfig(d, "workspace", config, "task")
	defer second()
	connections := func() []MCPReadinessSnapshot {
		var out []MCPReadinessSnapshot
		for _, snapshot := range d.mcpReadinessSnapshot() {
			if snapshot.Name == llm2jevMCPName && snapshot.InstanceID != "" {
				out = append(out, snapshot)
			}
		}
		return out
	}
	if snapshots := connections(); len(snapshots) != 1 {
		t.Fatalf("duplicate registration created extra entries: %+v", snapshots)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for connections()[0].State == MCPReadinessNotConfigured || connections()[0].State == MCPReadinessProbing {
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("readiness probe did not finish")
		}
	}
	if snapshots := connections(); snapshots[0].State != MCPReadinessReady || initializeCalls.Load() != 1 {
		t.Fatalf("duplicate registration created extra entries or probes: %+v / %d", snapshots, initializeCalls.Load())
	}
	first()
	first()
	if snapshots := connections(); len(snapshots) != 1 || snapshots[0].State != MCPReadinessReady {
		t.Fatalf("one owner cleanup removed the remaining owner's entry: %+v", snapshots)
	}
	second()
	for _, snapshot := range d.mcpReadinessSnapshot() {
		if snapshot.InstanceID != "" {
			t.Fatalf("last owner cleanup left a task instance: %+v", snapshot)
		}
	}
}

func TestMCPReadinessRetainsConcurrentTaskInstances(t *testing.T) {
	d := &Daemon{}
	first := registerTaskManagedMCPReadiness(d, "workspace", "same-name", "", "task", false)
	defer first()
	second := registerTaskManagedMCPReadiness(d, "workspace", "same-name", "", "task", false)
	defer second()
	snapshots := d.mcpReadinessSnapshot()
	var sameName int
	for _, snapshot := range snapshots {
		if snapshot.Name == "same-name" {
			sameName++
		}
	}
	if sameName != 2 {
		t.Fatalf("two tasks should remain visible; got %d (%+v)", sameName, snapshots)
	}
	second()
	snapshots = d.mcpReadinessSnapshot()
	sameName = 0
	for _, snapshot := range snapshots {
		if snapshot.Name == "same-name" {
			sameName++
		}
	}
	if sameName != 1 {
		t.Fatalf("cleanup removed another task: %+v", snapshots)
	}
}

func TestMCPReadinessDoesNotMergeDistinctEndpointsOrWorkspaces(t *testing.T) {
	d := &Daemon{}
	for _, registration := range []struct{ workspace, endpoint string }{
		{"workspace-a", "http://127.0.0.1:1234/task-a"},
		{"workspace-a", "http://127.0.0.1:1234/task-b"},
		{"workspace-b", "http://127.0.0.1:1234/task-a"},
	} {
		defer registerTaskManagedMCPReadiness(d, registration.workspace, "custom", registration.endpoint, "task", false)()
	}
	var count int
	for _, snapshot := range d.mcpReadinessSnapshot() {
		if snapshot.Name == "custom" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("distinct task or workspace connections merged: %+v", d.mcpReadinessSnapshot())
	}
}

func TestMCPReadinessCleanupCancelsInflightProbe(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			t.Log("handler canceled")
			close(canceled)
		case <-release:
			t.Log("handler released")
		}
	}))
	defer server.Close()
	defer close(release)
	cleanup := registerTaskManagedMCPReadiness(&Daemon{}, "workspace", "slow", server.URL, "task", true)
	defer cleanup()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cleanup()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("cleanup left probe running")
	}
}

func TestMCPReadinessRejectsInvalidInitialize(t *testing.T) {
	cases := map[string]string{
		"missing version":          `{"capabilities":{"tools":{}},"serverInfo":{"name":"x","version":"1"}}`,
		"unsupported version":      `{"protocolVersion":"2099-01-01","capabilities":{"tools":{}},"serverInfo":{"name":"x","version":"1"}}`,
		"missing capabilities":     `{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`,
		"missing tools capability": `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"x","version":"1"}}`,
		"invalid tools capability": `{"protocolVersion":"2024-11-05","capabilities":{"tools":true},"serverInfo":{"name":"x","version":"1"}}`,
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.Method == "notifications/initialized" {
					w.WriteHeader(202)
					return
				}
				body := json.RawMessage(result)
				if request.Method == "tools/list" {
					body = json.RawMessage(`{"tools":[]}`)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": body})
			}))
			defer server.Close()
			_, err := probeMCPInitializeAndTools(context.Background(), server.Client(), server.URL)
			if !errors.Is(err, errMCPProtocol) {
				t.Fatalf("invalid initialize accepted: %v", err)
			}
		})
	}
}

func TestMCPReadinessRPCRejectsWrongIDAndOversize(t *testing.T) {
	for name, body := range map[string]string{
		"wrong id":         `{"jsonrpc":"2.0","id":99,"result":{}}`,
		"string id":        `{"jsonrpc":"2.0","id":"1","result":{}}`,
		"oversized suffix": `{"jsonrpc":"2.0","id":1,"result":{}}` + strings.Repeat(" ", 256<<10) + "x",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			_, err := mcpReadinessRPC(context.Background(), server.Client(), server.URL, map[string]any{"id": 1}, true, "")
			if !errors.Is(err, errMCPProtocol) {
				t.Fatalf("invalid RPC accepted: %v", err)
			}
		})
	}
}
