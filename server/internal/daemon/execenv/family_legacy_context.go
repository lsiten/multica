package execenv

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/util"
)

// CleanupLegacyFamilyContext removes only daemon-owned context in the code
// directory. The caller must exclude all writers and publish family ownership
// before removing the old identity marker, which keeps interrupted adoption retryable.
func CleanupLegacyFamilyContext(root, workdir string) error {
	workdir, err := util.ResolveSymlinks(workdir)
	if err != nil {
		return err
	}
	workdirInfo, err := os.Stat(workdir)
	if err != nil {
		return err
	}
	path := filepath.Join(root, sidecarManifestFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		var manifest sidecarManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		canonical := func(path string) (string, bool) {
			rel := filepath.Base(path)
			for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
				info, err := os.Lstat(parent)
				if err != nil || info.Mode()&os.ModeSymlink != 0 {
					return "", false
				}
				if os.SameFile(info, workdirInfo) {
					return filepath.Join(workdir, rel), true
				}
				if filepath.Dir(parent) == parent {
					return "", false
				}
				rel = filepath.Join(filepath.Base(parent), rel)
			}
		}
		cleanPaths := func(paths []string) []string {
			result := make([]string, 0, len(paths))
			for _, path := range paths {
				if path, ok := canonical(path); ok {
					result = append(result, path)
				}
			}
			return result
		}
		manifest.Files, manifest.Dirs = cleanPaths(manifest.Files), cleanPaths(manifest.Dirs)
		replaced := manifest.ReplacedProviderFiles[:0]
		for _, file := range manifest.ReplacedProviderFiles {
			if path, ok := canonical(file.Path); ok {
				file.Path = path
				replaced = append(replaced, file)
			}
		}
		manifest.ReplacedProviderFiles = replaced
		filtered := sidecarsWithinWorkdir(&manifest, workdir)
		marker := filepath.Join(workdir, TaskContextMarkerRelPath)
		files := filtered.Files[:0]
		for _, file := range filtered.Files {
			if filepath.Clean(file) != marker {
				files = append(files, file)
			}
		}
		filtered.Files = files
		if err := rollBackManifest(*filtered, path); err != nil {
			return err
		}
	}
	for _, provider := range []string{"claude", "codex", "codebuddy", "qwen"} {
		path := runtimeConfigPath(workdir, provider)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := CleanupRuntimeConfig(workdir, provider); err != nil {
			return err
		}
	}
	return nil
}
