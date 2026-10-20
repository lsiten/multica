package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestMCPReadinessRetainsHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// Given an endpoint that responds over HTTP but cannot serve MCP.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			entry := &mcpReadinessEntry{endpoint: server.URL}

			// When the readiness handshake fails.
			probeMCPReadiness(context.Background(), server.Client(), entry)

			// Then diagnostics retain the HTTP status without exposing the endpoint.
			want := "http_status_404"
			if status == http.StatusBadGateway {
				want = "http_status_502"
			}
			if entry.Ready || entry.State != MCPReadinessOffline || entry.Reason != want {
				t.Fatalf("HTTP %d classified as state=%s reason=%s", status, entry.State, entry.Reason)
			}
		})
	}
}

type mcpReadinessTransport func(*http.Request) (*http.Response, error)

func (f mcpReadinessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestMCPReadinessRefreshesTaskEndpointAndStopsOnCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Given a task endpoint whose route is initially unavailable.
		var status, requests atomic.Int64
		status.Store(http.StatusNotFound)
		client := &http.Client{Transport: mcpReadinessTransport(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			current := int(status.Load())
			if current == 0 {
				return nil, errors.New("connection refused")
			}
			rec := httptest.NewRecorder()
			if current != http.StatusOK {
				rec.WriteHeader(current)
				return rec.Result(), nil
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				return nil, err
			}
			if request.Method == "notifications/initialized" {
				rec.WriteHeader(http.StatusAccepted)
			} else {
				result := map[string]any{"tools": []any{map[string]any{"name": "one"}}}
				if request.Method == "initialize" {
					result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}
				}
				writeLLM2JevMCPResult(rec, request.ID, result)
			}
			return rec.Result(), nil
		})}
		entry := &mcpReadinessEntry{MCPReadinessSnapshot: MCPReadinessSnapshot{Name: llm2jevMCPName}, endpoint: "http://127.0.0.1:1/task"}
		snapshot := func() MCPReadinessSnapshot {
			entry.mu.RLock()
			defer entry.mu.RUnlock()
			return entry.MCPReadinessSnapshot
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			monitorMCPReadiness(ctx, client, entry)
		}()
		time.Sleep(time.Second)
		synctest.Wait()
		if initial := snapshot(); initial.Ready || initial.State != MCPReadinessOffline || initial.Reason != "http_status_404" {
			t.Fatalf("initial failure: %+v", initial)
		}

		// When the route recovers, then later loses its listener.
		status.Store(http.StatusOK)
		time.Sleep(mcpReadinessProbeInterval + time.Second)
		synctest.Wait()
		if recovered := snapshot(); !recovered.Ready || recovered.State != MCPReadinessReady || recovered.ToolCount != 1 || recovered.Reason != "" {
			t.Fatalf("recovered route retained stale failure: %+v", recovered)
		}
		status.Store(0)
		time.Sleep(mcpReadinessProbeInterval + time.Second)
		synctest.Wait()
		if lost := snapshot(); lost.Ready || lost.State != MCPReadinessOffline || lost.ToolCount != 0 || lost.Reason != "endpoint_unreachable" {
			t.Fatalf("lost listener retained stale readiness: %+v", lost)
		}

		// Then cleanup joins the monitor and prevents further requests.
		cancel()
		<-done
		before := requests.Load()
		time.Sleep(2 * mcpReadinessProbeInterval)
		synctest.Wait()
		if after := requests.Load(); after != before {
			t.Fatalf("monitor continued after cleanup: before=%d after=%d", before, after)
		}
	})
}

func TestMCPReadinessPublishesTimeout(t *testing.T) {
	for _, backoff := range []bool{false, true} {
		name := "during request"
		if backoff {
			name = "during retry backoff"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Given a handshake that exhausts its deadline in either I/O or backoff.
				client := &http.Client{Transport: mcpReadinessTransport(func(r *http.Request) (*http.Response, error) {
					if backoff {
						time.Sleep(mcpReadinessProbeTimeout - 10*time.Millisecond)
						return nil, errors.New("connection reset")
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				})}
				entry := &mcpReadinessEntry{endpoint: "http://127.0.0.1:1/task"}

				// When the bounded handshake expires.
				probeMCPReadiness(t.Context(), client, entry)

				// Then it publishes a final timeout instead of remaining probing.
				if entry.Ready || entry.State != MCPReadinessTimeout || entry.Reason != "probe_timeout" || entry.CheckedAt == "" {
					t.Fatalf("timeout did not settle: %+v", entry.MCPReadinessSnapshot)
				}
			})
		})
	}
}

func TestMCPReadinessDoesNotRepeatExternalInitialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Given a user-configured local MCP whose initialization spends a session.
		requests := 0
		client := &http.Client{Transport: mcpReadinessTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result(), nil
		})}
		entry := &mcpReadinessEntry{MCPReadinessSnapshot: MCPReadinessSnapshot{Name: "external"}, endpoint: "http://127.0.0.1:1/task"}

		// When its initial bounded probe settles.
		ctx, cancel := context.WithTimeout(t.Context(), mcpReadinessProbeInterval+time.Second)
		defer cancel()
		monitorMCPReadiness(ctx, client, entry)
		initial := requests
		time.Sleep(2 * mcpReadinessProbeInterval)

		// Then it does not consume more sessions on the built-in refresh schedule.
		if initial != 3 || requests != initial {
			t.Fatalf("external initialization repeated: initial=%d total=%d", initial, requests)
		}
	})
}
