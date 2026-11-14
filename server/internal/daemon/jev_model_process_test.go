package daemon

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type unavailableModels struct{ modelservice.Backend }

func TestBackendUsableRejectsTypedNil(t *testing.T) {
	var backend *modelservice.Client
	if backendUsable(backend) {
		t.Fatal("typed-nil model backend must not be admitted")
	}
	if !backendUsable(unavailableModels{}) {
		t.Fatal("non-nil model backend was rejected")
	}
}

func (unavailableModels) Catalog(context.Context) ([]jevmodels.Model, error) {
	return nil, errors.New("model child unavailable")
}

func TestJevModelManagerFailsWhenProcessLaunchUnavailable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ProcessServices: []string{"ai", "mirror", "application", "environment", "gateway"}}, client: NewClient(backend.URL)}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("unavailable ai process must not fall back to an in-process manager")
	}
}

func TestJevModelManagerRetriesChildWithoutLocalFallback(t *testing.T) {
	var probes int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			probes++
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ProcessServices: []string{"ai", "mirror", "application", "environment", "gateway"}}, client: NewClient(backend.URL)}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("first child launch failure was hidden")
	}
	d.jevModelRetryAt = time.Time{}
	if _, err := d.jevModelManager(); err == nil {
		t.Fatal("retry child launch failure was hidden")
	}
	if probes < 2 {
		t.Fatalf("expected the ai account to be re-probed, got %d probes", probes)
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
