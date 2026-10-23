package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cleanupRunPrivateSidecars reclaims only manifest-owned private configuration.
// The caller owns the run root; shared code and unrecorded user files survive.
func cleanupRunPrivateSidecars(envRoot string) error {
	if envRoot == "" {
		return nil
	}
	root, err := os.OpenRoot(envRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := root.ReadFile(sidecarManifestFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var manifest sidecarManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("read private sidecar ownership: %w", err)
	}
	namespaces := []string{"task-context", "cursor-mcp-plugin"}
	for _, directory := range manifest.Dirs {
		if filepath.Clean(filepath.Dir(directory)) == filepath.Clean(envRoot) && (strings.HasPrefix(filepath.Base(directory), "cursor-mcp-plugin-") || strings.HasPrefix(filepath.Base(directory), "task-context-")) {
			namespaces = append(namespaces, filepath.Base(directory))
		}
	}
	for _, namespace := range namespaces {
		info, err := root.Lstat(namespace)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private configuration namespace is not a directory: %s", namespace)
		}
		privateRoot, err := root.OpenRoot(namespace)
		if err != nil {
			return err
		}
		selected := sidecarsWithinWorkdir(&manifest, filepath.Join(envRoot, namespace))
		cleanupErr := removePrivateSidecars(privateRoot, filepath.Join(envRoot, namespace), selected)
		closeErr := privateRoot.Close()
		if cleanupErr != nil {
			return cleanupErr
		}
		if closeErr != nil {
			return closeErr
		}
		// Only generated namespace roots recorded by this attempt may be removed.
		if strings.HasPrefix(namespace, "cursor-mcp-plugin-") || strings.HasPrefix(namespace, "task-context-") {
			if err := root.Remove(namespace); err != nil && !errors.Is(err, os.ErrNotExist) {
				entries, readErr := os.ReadDir(filepath.Join(envRoot, namespace))
				if readErr != nil {
					return readErr
				}
				if len(entries) == 0 {
					return err
				}
			}
		}
	}
	return nil
}

func removePrivateSidecars(root *os.Root, base string, manifest *sidecarManifest) error {
	// A recorded directory may have been replaced with a link to unrecorded
	// user content inside this namespace. Validate ancestors before any removal.
	for _, paths := range [][]string{manifest.Files, manifest.Dirs} {
		for _, path := range paths {
			relative, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
				info, err := root.Lstat(parent)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("private sidecar ancestor is a symlink: %s", parent)
				}
			}
		}
	}
	for _, path := range manifest.Files {
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove private sidecar: %w", err)
		}
	}
	for i := len(manifest.Dirs) - 1; i >= 0; i-- {
		relative, err := filepath.Rel(base, manifest.Dirs[i])
		if err != nil {
			return err
		}
		if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
			// A user-added child makes this directory theirs to retain.
			directory, openErr := root.Open(relative)
			if openErr != nil {
				return fmt.Errorf("inspect private sidecar directory: %w", openErr)
			}
			entries, readErr := directory.ReadDir(1)
			closeErr := directory.Close()
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if len(entries) == 0 {
				return fmt.Errorf("remove private sidecar directory: %w", err)
			}
		}
	}
	return nil
}
