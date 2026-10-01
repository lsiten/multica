package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/jevmodels"
)

func (d *Daemon) jevModelManager() (*jevmodels.Manager, error) {
	d.jevModelsOnce.Do(func() {
		profile := strings.NewReplacer("/", "_", "\\", "_").Replace(d.cfg.Profile)
		if profile == "" {
			profile = "default"
		}
		root := filepath.Join(d.cfg.WorkspacesRoot, ".jev-models", profile)
		python := os.Getenv("MULTICA_JEV_PYTHON")
		if python == "" {
			python = "python3"
		}
		d.jevModels, d.jevModelsErr = jevmodels.New(context.Background(), jevmodels.Config{RootDir: root, PythonPath: python})
	})
	return d.jevModels, d.jevModelsErr
}

func (d *Daemon) closeJevModelManager() {
	if d.jevModels != nil {
		if err := d.jevModels.Close(); err != nil {
			d.logger.Warn("jev model manager close failed", "error", err)
		}
	}
}

func jevModelConfigError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("jev model manager: %w", err)
}
