package daemon

import (
	"context"
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
	if d.jevModels != nil {
		return d.jevModels, nil
	}
	if time.Now().Before(d.jevModelRetryAt) {
		return nil, d.jevModelsErr
	}
	defer func() {
		if d.jevModelsErr != nil {
			d.jevModelFailures++
			d.jevModelRetryAt = time.Now().Add(reportRetryDelay(d.jevModelFailures, d.jevModelsErr))
		} else {
			d.jevModelFailures = 0
			d.jevModelRetryAt = time.Time{}
		}
	}()
	d.jevModelsErr = nil
	func() {
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
			// Attempt the separate process first. Any failure — account
			// unavailable, a launch error, or a live prior owner that requires
			// reconciliation — falls through to the in-process manager below so
			// the JEV model is still served by the daemon. This is the same
			// fail-closed behaviour environment/application/gateway use: a
			// separate-process start failure must never leave the model
			// unavailable, and must not abort the whole daemon.
			account := struct {
				ID string `json:"id"`
			}{ID: d.accountID}
			if account.ID == "" && d.client != nil {
				// The account only names the process scope; the in-process
				// manager below needs neither it nor the network, so a failed
				// lookup is a reason to fall back, not to fail closed.
				query, cancel := context.WithTimeout(d.daemonLifecycleCtx(), 5*time.Second)
				_ = d.client.getJSON(query, "/api/me", &account)
				cancel()
			}
			if account.ID != "" {
				// The deferred daemon close drains this process before it exits.
				// Passing the canceled run context here would kill it before
				// authenticated Stop can clean up.
				d.jevModels, d.jevModelsErr = modelservice.Launch(context.Background(), root, runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "ai"}, d.cfg.NativeHostExecutable, d.cfg.NativeHostBuild)
				if d.jevModels != nil {
					return
				}
				// The separate process could not start: fall through to the
				// in-process manager below instead of leaving the model
				// unavailable.
			}
		}
		manager, err := jevmodels.New(context.Background(), jevmodels.Config{RootDir: root, PythonPath: python})
		d.jevModelsErr = err
		if err == nil {
			d.jevModels = &modelservice.Local{Manager: manager}
		}
	}()
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
