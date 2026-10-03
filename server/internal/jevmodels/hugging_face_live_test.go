package jevmodels

import (
	"os"
	"testing"
)

func TestLiveHubMetadata(t *testing.T) {
	if os.Getenv("MULTICA_RUN_HUB_METADATA_SMOKE") != "1" {
		t.Skip("explicit Hub metadata smoke only")
	}
	m := newTestManager(t)
	model, err := m.Register(t.Context(), "Mapika/decider-4b", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.Revision == "main" || model.DownloadBytes <= 0 {
		t.Fatal(model)
	}
	t.Logf("Resolved %s at %s (%d bytes); no model weights downloaded", model.ID, model.Revision, model.DownloadBytes)
}
