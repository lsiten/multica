package jevmodels

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed worker.py
var workerSource []byte

const engineWheel = "https://files.pythonhosted.org/packages/59/46/72898a32aca3ed95d3807304c10d3635d0448d00a4f68ed878e7de4d0571/decider_ai-1.8.1-py3-none-any.whl#sha256=ac9414054b29a44d34eae1b057844cf8f997e41cf2e8ee952320fb12ba35bb55"

func (m *Manager) engineDir() string { return filepath.Join(m.cfg.RootDir, "engine-"+EngineVersion) }
func (m *Manager) python() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.engineDir(), "Scripts", "python.exe")
	}
	return filepath.Join(m.engineDir(), "bin", "python")
}

func (m *Manager) installEngine(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(m.engineDir(), "ready")); err == nil {
		return nil
	}
	log, err := os.OpenFile(filepath.Join(m.cfg.RootDir, "install.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	steps := [][]string{
		{m.cfg.PythonPath, "-I", "-m", "venv", m.engineDir()},
		{m.python(), "-I", "-m", "pip", "--isolated", "install", "--index-url", "https://pypi.org/simple", "--disable-pip-version-check", "--only-binary=:all:", "decider-ai[serve] @ " + engineWheel},
		{m.python(), "-I", "-c", "import importlib.metadata as m; import decider.serve; assert m.version('decider-ai') == '" + EngineVersion + "'"},
	}
	for _, args := range steps {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = cleanEnvironment()
		cmd.Stdout = log
		cmd.Stderr = log
		if err = cmd.Run(); err != nil {
			return fmt.Errorf("install Jev engine failed (see daemon model install.log): %w", err)
		}
	}
	return os.WriteFile(filepath.Join(m.engineDir(), "ready"), []byte(EngineVersion), 0600)
}

func cleanEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "CUDA_VISIBLE_DEVICES", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR":
			result = append(result, entry)
		}
	}
	return result
}
