package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func (d *Daemon) jevModelManager() (modelservice.Backend, error) {
	d.jevModelsMu.Lock()
	defer d.jevModelsMu.Unlock()
	if d.jevModelsClosing {
		return nil, jevmodels.ErrClosed
	}
	if backendUsable(d.jevModels) {
		return d.jevModels, nil
	}
	if d.jevModels != nil {
		d.jevModels = nil
		if d.jevModelsErr == nil {
			d.jevModelsErr = errors.New("ai process backend is unavailable")
		}
	}
	if !slices.Contains(d.cfg.ProcessServices, "ai") {
		return nil, errors.New("required ai process owner is not configured")
	}
	if time.Now().Before(d.jevModelRetryAt) {
		return nil, d.jevModelsErr
	}

	profile := strings.NewReplacer("/", "_", "\\", "_").Replace(d.cfg.Profile)
	if profile == "" {
		profile = "default"
	}
	root := filepath.Join(d.cfg.WorkspacesRoot, ".jev-models", profile)
	account := struct {
		ID string `json:"id"`
	}{ID: d.accountID}
	if account.ID == "" && d.client != nil {
		query, cancel := context.WithTimeout(d.daemonLifecycleCtx(), 5*time.Second)
		_ = d.client.getJSON(query, "/api/me", &account)
		cancel()
	}
	processErr := error(nil)
	if account.ID == "" {
		processErr = errors.New("ai process account unavailable")
	} else {
		// The child owns the model manager. Keep its launch context independent
		// from the daemon request so shutdown can perform an authenticated stop.
		d.jevModels, processErr = modelservice.Launch(context.Background(), root, runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "ai"}, d.cfg.NativeHostExecutable, d.cfg.NativeHostBuild)
	}
	if backendUsable(d.jevModels) {
		d.jevModelFailures = 0
		d.jevModelRetryAt = time.Time{}
		return d.jevModels, nil
	}
	d.jevModelFailures++
	d.jevModelRetryAt = time.Now().Add(reportRetryDelay(d.jevModelFailures, processErr))
	d.jevModelsErr = processErr
	return nil, d.jevModelsErr
}

func backendUsable(backend modelservice.Backend) bool {
	if backend == nil {
		return false
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func (d *Daemon) closeJevModelManager() {
	d.jevModelsMu.Lock()
	defer d.jevModelsMu.Unlock()
	d.jevModelsClosing = true
	if backendUsable(d.jevModels) {
		if err := d.jevModels.Close(); err != nil {
			if d.logger != nil {
				d.logger.Warn("jev model manager close failed", "error", err)
			}
		}
	}
}

func jevModelConfigError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("jev model manager: %w", err)
}
