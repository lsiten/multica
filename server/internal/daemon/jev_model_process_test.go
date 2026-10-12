package daemon

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableModels struct{ modelservice.Backend }

func (unavailableModels) Catalog(context.Context) ([]jevmodels.Model, error) {
	return nil, errors.New("model child unavailable")
}

func TestJevModelManagerRetriesFailedIdentityResolution(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ProcessServices: []string{"ai"}}, client: NewClient(backend.URL)}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("unavailable identity unexpectedly initialized model service")
	}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("cached transient failure disappeared before retry deadline")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("failure backoff did not collapse repeat probes: %d", got)
	}
	if !d.jevModelRetryAt.After(time.Now()) {
		t.Fatal("retry deadline missing")
	}
	d.jevModelRetryAt = time.Time{}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("fixture outage ignored on retry")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("identity requests = %d, want retry on second attempt", got)
	}
}

func TestJevModelManagerKeepsInitializedBackendWithoutRemoteProbe(t *testing.T) {
	backend := unavailableModels{}
	d := &Daemon{jevModels: backend, cfg: Config{ProcessServices: []string{"ai"}}}
	if got, err := d.jevModelManager(); err != nil || got != backend {
		t.Fatalf("initialized backend was replaced by remote availability: %v", err)
	}
}
func TestJevModelRemoteCatalogFailureIsExplicit(t *testing.T) {
	d := &Daemon{client: NewClient("http://fixture.invalid"), jevModels: unavailableModels{}}
	d.client.SetToken("fixture-only-token")
	req := httptest.NewRequest("GET", "/jev/models", nil)
	req.Header.Set("Authorization", "Bearer fixture-only-token")
	response := httptest.NewRecorder()
	d.jevModelsHandler()(response, req)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failure hidden as empty catalog: %d %s", response.Code, response.Body)
	}
}
