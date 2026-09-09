package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteReaderCancellationUsesClaimAndStopsWatcher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.LocalReviewStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ClaimToken != "claim" || r.URL.Path != "/api/daemon/runtimes/runtime/local-reviews/relay/request/status" {
			t.Error("wrong claim status request")
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"active":false}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	d := &Daemon{client: NewClient(server.URL)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		d.watchReviewReader(ctx, protocol.LocalReviewCommand{ID: "request", RuntimeID: "runtime", ClaimToken: "claim"}, cancel)
	}()
	select {
	case <-stopped:
		if ctx.Err() != context.Canceled {
			t.Fatal("read was not cancelled")
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-stopped
		t.Fatal("watcher did not stop after reader disconnected")
	}
}
