package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestReportTaskUsage_Retry(t *testing.T) {
	defer noSleepRetry(t)()
	usage := []TaskUsageEntry{
		{Provider: "codex", Model: "model-a", InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50},
		{Provider: "claude", Model: "model-b", InputTokens: 30, CacheWriteTokens: 10, CostUSDTicks: 900},
	}
	for _, tc := range []struct {
		name       string
		status     int
		failures   int32
		wantCalls  int32
		wantError  bool
		cancelled  bool
		emptyUsage bool
		loseReply  bool
	}{
		{name: "server recovers", status: http.StatusServiceUnavailable, failures: 2, wantCalls: 3},
		{name: "rate limit recovers", status: http.StatusTooManyRequests, failures: 1, wantCalls: 2},
		{name: "request timeout recovers", status: http.StatusRequestTimeout, failures: 1, wantCalls: 2},
		{name: "lost response recovers", failures: 1, wantCalls: 2, loseReply: true},
		{name: "bad request is permanent", status: http.StatusBadRequest, failures: 1, wantCalls: 1, wantError: true},
		{name: "unauthorized is permanent", status: http.StatusUnauthorized, failures: 1, wantCalls: 1, wantError: true},
		{name: "retry budget exhausted", status: http.StatusServiceUnavailable, failures: 100, wantCalls: int32(len(defaultTerminalRetrySchedule) + 1), wantError: true},
		{name: "cancelled context", cancelled: true, wantError: true},
		{name: "no usage", emptyUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/daemon/tasks/task-1/usage" {
					t.Errorf("request = %s %s, want task usage POST", r.Method, r.URL.Path)
				}
				var body struct {
					Usage []TaskUsageEntry `json:"usage"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode attempt %d: %v", call, err)
				} else if !reflect.DeepEqual(body.Usage, usage) {
					t.Errorf("attempt %d usage = %+v, want %+v", call, body.Usage, usage)
				}
				if call <= tc.failures {
					if tc.loseReply {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Errorf("hijack response: %v", err)
							return
						}
						if err := conn.Close(); err != nil {
							t.Errorf("close response connection: %v", err)
						}
						return
					}
					w.WriteHeader(tc.status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			entries := usage
			if tc.emptyUsage {
				entries = nil
			}
			err := NewClient(srv.URL).ReportTaskUsage(ctx, "task-1", entries)
			if (err != nil) != tc.wantError {
				t.Fatalf("ReportTaskUsage error = %v, wantError %v", err, tc.wantError)
			}
			if tc.wantError && !tc.cancelled {
				var reqErr *requestError
				if !errors.As(err, &reqErr) || reqErr.StatusCode != tc.status {
					t.Errorf("error = %v, want HTTP %d", err, tc.status)
				}
			}
			if got := calls.Load(); got != tc.wantCalls {
				t.Fatalf("attempts = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}
