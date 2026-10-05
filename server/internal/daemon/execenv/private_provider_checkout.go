package execenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// NeedsPrivateProviderCheckout identifies providers whose per-run settings
// are read from a fixed project path rather than an isolated configuration root.
func NeedsPrivateProviderCheckout(provider string, config json.RawMessage) bool {
	managed := len(bytes.TrimSpace(config)) > 0 && !bytes.Equal(bytes.TrimSpace(config), []byte("null"))
	return provider == "reasonix" || (managed && (provider == "cursor" || provider == "omp"))
}

type replacedProviderFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode"`
}

func isolatePrivateProviderFile(dir, provider string, config json.RawMessage, manifest *sidecarManifest) error {
	if !NeedsPrivateProviderCheckout(provider, config) {
		return nil
	}
	relative := "reasonix.toml"
	if provider == "cursor" {
		dir = cursorProjectRoot(dir)
		relative = filepath.Join(".cursor", "mcp.json")
	}
	if provider == "omp" {
		relative = filepath.Join(".omp", "mcp.json")
	}
	parent := filepath.Join(dir, filepath.Dir(relative))
	if info, err := os.Lstat(parent); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("private provider config directory is not a regular directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	path := filepath.Join(dir, relative)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private provider config %s is not a regular file", relative)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	manifest.ReplacedProviderFiles = append(manifest.ReplacedProviderFiles, replacedProviderFile{Path: path, Content: data, Mode: uint32(info.Mode().Perm())})
	return os.Remove(path)
}
