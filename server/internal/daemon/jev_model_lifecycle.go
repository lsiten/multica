package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	// The separate ai process is the preferred owner; once started it is the
	// only backend. It is re-probed below on the backoff so a transient
	// separate-process failure does not permanently mask it with the
	// in-process fallback (an account that was unreachable at startup
	// becomes reachable later).
	if backendUsable(d.jevModels) {
		return d.jevModels, nil
	}
	if d.jevModels != nil {
		// An interface can be non-nil while holding a nil *Client after a
		// failed/restarted AI process. Treat that state as unavailable instead
		// of allowing the task runner to call through a nil receiver.
		d.jevModels = nil
		if d.jevModelsErr == nil {
			d.jevModelsErr = errors.New("ai model backend is unavailable")
		}
	}
	// The in-process fallback bridges a separate-process failure so the model
	// stays available (fail-closed). While the backoff has not elapsed keep
	// returning it instead of a retry error; when it has elapsed re-probe the
	// separate process below rather than reusing the fallback forever, so the
	// split service is eventually used instead of being masked in-process.
	if backendUsable(d.jevModelsFallback) {
		if time.Now().Before(d.jevModelRetryAt) {
			return d.jevModelsFallback, nil
		}
	} else if time.Now().Before(d.jevModelRetryAt) {
		return nil, d.jevModelsErr
	}
	if d.jevModelsFallback != nil && !backendUsable(d.jevModelsFallback) {
		d.jevModelsFallback = nil
	}
	profile := strings.NewReplacer("/", "_", "\\", "_").Replace(d.cfg.Profile)
	if profile == "" {
		profile = "default"
	}
	root := filepath.Join(d.cfg.WorkspacesRoot, ".jev-models", profile)
	python := os.Getenv("MULTICA_JEV_PYTHON")
	if python == "" {
		python = "python3"
	}
	// A failed account lookup only names the process scope; the in-process
	// fallback below needs neither it nor the network, so it is a reason to
	// fall back, not to fail closed.
	processErr := error(nil)
	if slices.Contains(d.cfg.ProcessServices, "ai") {
		account := struct {
			ID string `json:"id"`
		}{ID: d.accountID}
		if account.ID == "" && d.client != nil {
			query, cancel := context.WithTimeout(d.daemonLifecycleCtx(), 5*time.Second)
			_ = d.client.getJSON(query, "/api/me", &account)
			cancel()
		}
		if account.ID != "" {
			// The deferred daemon close drains this process before it exits;
			// passing the canceled run context here would kill it before
			// authenticated Stop can clean up.
			d.jevModels, processErr = modelservice.Launch(context.Background(), root, runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "ai"}, d.cfg.NativeHostExecutable, d.cfg.NativeHostBuild)
			if backendUsable(d.jevModels) {
				// The split process recovered: retire the bridging fallback so
				// the split service is used, not the in-process one.
				if d.jevModelsFallback != nil {
					_ = d.jevModelsFallback.Close()
					d.jevModelsFallback = nil
				}
				d.jevModelFailures = 0
				d.jevModelRetryAt = time.Time{}
				return d.jevModels, nil
			}
		} else {
			processErr = errors.New("ai process account unavailable")
		}
	}
	// Bridge the separate-process failure with the in-process manager so the
	// model stays available; it is re-probed on the backoff and is never
	// cached as the permanent owner, so the split service is not masked.
	if !backendUsable(d.jevModelsFallback) {
		manager, err := jevmodels.New(context.Background(), jevmodels.Config{RootDir: root, PythonPath: python})
		if err == nil {
			d.jevModelsFallback = &modelservice.Local{Manager: manager}
		} else {
			processErr = err
		}
	}
	// The split process is not (yet) available: schedule a re-probe so the
	// fallback never permanently masks it. The fallback keeps the model
	// available, so the timer drives a re-probe rather than a surfaced failure.
	d.jevModelFailures++
	d.jevModelRetryAt = time.Now().Add(reportRetryDelay(d.jevModelFailures, processErr))
	if backendUsable(d.jevModelsFallback) {
		d.jevModelsErr = nil
		return d.jevModelsFallback, nil
	}
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
	if backendUsable(d.jevModelsFallback) {
		if err := d.jevModelsFallback.Close(); err != nil {
			if d.logger != nil {
				d.logger.Warn("jev model fallback close failed", "error", err)
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
