package smokefixture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// PerformanceMode is confined to the nonce-owned fixture and never changes input/takeover mode.
type PerformanceMode struct {
	SourceTag  uint32 `json:"source_tag"`
	LifetimeMS int64  `json:"lifetime_ms"`
}
type PerformanceMarker struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	CellSize  float64 `json:"cell_size"`
	Columns   int     `json:"columns"`
	SourceTag uint32  `json:"source_tag"`
}

// PreparePerformance configures animation before the newly prepared App is launched.
func PreparePerformance(executable, evidence string, mode PerformanceMode) (result *App, runErr error) {
	if mode.SourceTag == 0 || mode.LifetimeMS < 1000 || mode.LifetimeMS > 2700000 {
		return nil, errors.New("invalid_performance_fixture_mode")
	}
	app, err := Prepare(executable, evidence)
	if err != nil {
		return nil, err
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, app.Stop(context.Background()))
		}
	}()
	path := filepath.Join(app.bundle, "Contents", "fixture.json")
	raw, err := readPrivate(path, 4096)
	if err != nil {
		return nil, err
	}
	var cfg configuration
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	cfg.Performance = &mode
	raw, err = json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return nil, err
	}
	return app, nil
}
