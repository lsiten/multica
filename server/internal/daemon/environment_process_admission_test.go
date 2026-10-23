package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEnvironmentOversizeRejectedBeforeDispatchDoesNotProtectScope(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			calls.Add(1)
			return json.RawMessage(`{}`), nil
		}
	})
	err := c.mutation(t.Context(), "environment.bind", map[string]string{"large": strings.Repeat("x", 256<<10)}, nil)
	c.mu.Lock()
	tracked := len(c.pending)
	c.mu.Unlock()
	status, healthErr := c.process.Client.Health(t.Context())
	t.Logf("local rejection=%v; client pending=%d; durable receipts=%d; handler calls=%d; health=%v", err, tracked, status.Receipts.Total, calls.Load(), healthErr)
	if tracked != 0 {
		t.Error("request rejected by local size check created an uncertain write")
	}
	if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); err != nil {
		t.Errorf("valid next write blocked by unadmitted oversized request: %v", err)
	}
}

func TestEnvironmentRejectedAcknowledgementRefreshesWithoutUnknownState(t *testing.T) {
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			return json.RawMessage(`{}`), nil
		}
	})
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend := c.process.Client
	upstream := record.Address
	var rejected atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req runtimeproc.Request
		if err = json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		if req.Operation == "acknowledge" && rejected.CompareAndSwap(false, true) {
			status, err := backend.Health(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			bump, err := backend.Request("resume", status.Fence, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = backend.Call(t.Context(), bump); err != nil {
				t.Error(err)
				return
			}
		}
		forward, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream+"/rpc", bytes.NewReader(body))
		if err != nil {
			t.Error(err)
			return
		}
		forward.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(forward)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	record.Address = proxy.URL
	c.process.Client, err = runtimeproc.NewClient(record)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	waiting := c.acknowledgement != nil
	consumed := len(c.consumed)
	retirementErr := c.retirementErr
	c.mu.Unlock()
	status, err := backend.Health(t.Context())
	t.Logf("explicit stale-fence ACK rejection: unknownACK=%v consumed=%d completed=%d pending=%d retirement=%v health=%v", waiting, consumed, status.Receipts.Completed, status.Receipts.Pending, retirementErr, err)
	if waiting || consumed > 0 {
		t.Error("definitely unadmitted ACK stalls completed-receipt retirement until its deadline")
	}
}
