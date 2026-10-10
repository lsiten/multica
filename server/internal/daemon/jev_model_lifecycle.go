package daemon

import (
	"context"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/jevmodels"
)

func (d *Daemon) jevModelManager() (modelservice.Backend, error) {
	d.jevModelsMu.Lock()
	defer d.jevModelsMu.Unlock()
	if d.jevModelsClosing {
		return nil, jevmodels.ErrClosed
	}
	d.jevModelsOnce.Do(func() {
		if err := cli.ValidateProcessServices(d.cfg.ProcessServices); err != nil {
			d.jevModelsErr = err
			return
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
		if slices.Contains(d.cfg.ProcessServices, "ai") {
			query, cancel := context.WithTimeout(d.daemonLifecycleCtx(), 5*time.Second)
			defer cancel()
			var account struct {
				ID string `json:"id"`
			}
			if d.client == nil {
				d.jevModelsErr = errors.New("model process account unavailable")
				return
			}
			if err := d.client.getJSON(query, "/api/me", &account); err != nil {
				d.jevModelsErr = fmt.Errorf("model process account: %w", err)
				return
			}
			if account.ID == "" {
				d.jevModelsErr = errors.New("model process account identity missing")
				return
			}
			// The deferred daemon close drains this process before it exits. Passing the
			// canceled run context here would kill it before authenticated Stop can clean up.
			d.jevModels, d.jevModelsErr = modelservice.Launch(context.Background(), root, runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "ai"}, d.cfg.NativeHostExecutable, d.cfg.NativeHostBuild)
			return
		}
		manager, err := jevmodels.New(context.Background(), jevmodels.Config{RootDir: root, PythonPath: python})
		d.jevModelsErr = err
		if err == nil {
			d.jevModels = &modelservice.Local{Manager: manager}
		}
	})
	return d.jevModels, d.jevModelsErr
}

func (d *Daemon) closeJevModelManager() {
	d.jevModelsMu.Lock()
	defer d.jevModelsMu.Unlock()
	d.jevModelsClosing = true
	if d.jevModels != nil {
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
