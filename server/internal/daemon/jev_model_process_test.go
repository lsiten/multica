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
func TestJevModelRemoteCatalogFailureIsExplicit(t *testing.T) {
	d := &Daemon{client: NewClient("http://fixture.invalid"), jevModels: unavailableModels{}}
	d.client.SetToken("fixture-only-token")
	d.jevModelsOnce.Do(func() {})
	req := httptest.NewRequest("GET", "/jev/models", nil)
	req.Header.Set("Authorization", "Bearer fixture-only-token")
	response := httptest.NewRecorder()
	d.jevModelsHandler()(response, req)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failure hidden as empty catalog: %d %s", response.Code, response.Body)
	}
}
