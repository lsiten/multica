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
	// The in-process manager now bridges the failed ai process. While the
	// backoff has not elapsed the same fallback is returned so the model stays
	// available; the split process is re-probed only after the backoff elapses.
	if _, err := d.jevModelManager(); err != nil {
		t.Fatalf("initialized in-process manager was replaced: %v", err)
	}
	if got, _ := d.jevModelManager(); got == nil {
		t.Fatal("expected an initialized in-process backend")
	}
	if d.jevModelsFallback == nil {
		t.Fatal("expected the in-process backend to be the bridging fallback, not a permanent owner")
	}
}

// TestJevModelManagerReprobesAccountAfterFallback verifies the in-process
// fallback bridges a separate-process failure without permanently masking it:
// once the backoff elapses the ai account is re-probed instead of the fallback
// being reused forever.
func TestJevModelManagerReprobesAccountAfterFallback(t *testing.T) {
	var probes int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ProcessServices: []string{"ai"}}, client: NewClient(backend.URL)}
	if _, err := d.jevModelManager(); err != nil {
		t.Fatalf("unavailable ai process should fall back to the in-process manager, got %v", err)
	}
	if d.jevModelsFallback == nil {
		t.Fatal("expected an in-process fallback to bridge the ai process failure")
	}
	// Advance the retry timer into the past so the next call re-probes the
	// account instead of returning the cached fallback without a re-probe.
	d.jevModelRetryAt = time.Time{}
	if _, err := d.jevModelManager(); err != nil {
		t.Fatalf("fallback should bridge the retry without surfacing a failure, got %v", err)
	}
	if probes < 2 {
		t.Fatalf("expected the ai account to be re-probed after the backoff, got %d probes", probes)
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
