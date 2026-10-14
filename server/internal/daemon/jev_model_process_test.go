package daemon

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unavailableModels struct{ modelservice.Backend }

func (unavailableModels) Catalog(context.Context) ([]jevmodels.Model, error) {
	return nil, errors.New("model child unavailable")
}

func TestJevModelManagerFallsBackToInProcessWhenProcessLaunchFails(t *testing.T) {
	// When the ai process cannot be started (here: its account is unreachable,
	// so the process scope can never be built), the daemon must fall back to
	// the in-process manager instead of leaving the model unavailable or
	// aborting. This mirrors environment/application/gateway fail-closed.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ProcessServices: []string{"ai"}}, client: NewClient(backend.URL)}
	if _, err := d.jevModelManager(); err != nil {
		t.Fatalf("unavailable ai process should fall back to the in-process manager, got %v", err)
	}
	// The in-process manager is now initialized and reused on subsequent calls
	// without re-probing the unreachable account.
	if _, err := d.jevModelManager(); err != nil {
		t.Fatalf("initialized in-process manager was replaced: %v", err)
	}
	if got, _ := d.jevModelManager(); got == nil {
		t.Fatal("expected an initialized in-process backend")
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
