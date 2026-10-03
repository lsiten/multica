package execenv

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type archivedDirectoryMode struct {
	path     string
	mode     os.FileMode
	modified time.Time
}

// RestoreEnvironmentArchive reconstructs into a private staging directory, then
// publishes without replacing any existing environment, including an empty one.
func RestoreEnvironmentArchive(ctx context.Context, workspacesRoot, id, profile, backendURL string) (EnvironmentArchive, error) {
	manifest, err := ReadEnvironmentArchive(workspacesRoot, id)
	if err != nil {
		return manifest, err
	}
	if manifest.Profile != profile || manifest.BackendURL != backendURL {
		return manifest, errors.New("archive profile does not match runtime")
	}
	workspace, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return manifest, err
	}
	defer workspace.Close()
	if _, err := workspace.Lstat(filepath.FromSlash(manifest.RelativeRoot)); !errors.Is(err, os.ErrNotExist) {
		return manifest, errors.New("restore destination already exists or is unavailable")
	}
	parent, err := environmentArchiveParent(workspacesRoot)
	if err != nil {
		return manifest, err
	}
	defer parent.Close()
	archive, err := parent.OpenRoot(id)
	if err != nil {
		return manifest, err
	}
	defer archive.Close()
	if err := verifyEnvironmentArchiveRoot(ctx, archive, manifest); err != nil {
		return manifest, err
	}
	stagingName := ".restore-" + rand.Text()
	staging, err := openReviewArchiveChild(parent, stagingName)
	if err != nil {
		return manifest, err
	}
	defer staging.Close()
	defer parent.RemoveAll(stagingName)
	modes, err := extractEnvironmentArchive(ctx, archive, staging)
	if err != nil {
		return manifest, err
	}
	stagingPath := filepath.Join(workspacesRoot, environmentArchiveDir, stagingName)
	environmentPath := filepath.Join(stagingPath, "environment")
	for _, repository := range manifest.Repositories {
		if err := restoreGitRepository(ctx, environmentPath, filepath.Join(stagingPath, filepath.FromSlash(repository.RecoveryPath)), repository); err != nil {
			return manifest, err
		}
		if _, err := archiveGit(ctx, filepath.Join(environmentPath, filepath.FromSlash(repository.Path)), "fsck", "--full"); err != nil {
			return manifest, err
		}
	}
	for index := len(modes) - 1; index >= 0; index-- {
		directory := modes[index]
		if err := staging.Chmod(directory.path, directory.mode); err != nil {
			return manifest, err
		}
		if err := staging.Chtimes(directory.path, directory.modified, directory.modified); err != nil {
			return manifest, err
		}
	}
	physicalWorkspace := filepath.Dir(filepath.FromSlash(manifest.RelativeRoot))
	if err := workspace.Mkdir(physicalWorkspace, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return manifest, err
	}
	info, err := workspace.Lstat(physicalWorkspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("restore workspace is not a real directory")
	}
	destinationParent, err := workspace.OpenRoot(physicalWorkspace)
	if err != nil {
		return manifest, err
	}
	defer destinationParent.Close()
	opened, err := destinationParent.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return manifest, errors.New("restore workspace identity changed")
	}
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	environment, err := staging.OpenRoot("environment")
	if err != nil {
		return manifest, err
	}
	defer environment.Close()
	if info, err := environment.Lstat(environmentRestoreMarker); err == nil && !info.Mode().IsRegular() {
		return manifest, errors.New("invalid restoration marker")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return manifest, err
	}
	marker, err := environment.OpenFile(environmentRestoreMarker, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return manifest, err
	}
	writeErr := json.NewEncoder(marker).Encode(time.Now().UTC())
	if err := errors.Join(writeErr, marker.Sync(), marker.Close()); err != nil {
		return manifest, err
	}
	if err := renameArchiveDirectoryNoReplace(staging, "environment", destinationParent, filepath.Base(filepath.FromSlash(manifest.RelativeRoot))); err != nil {
		return manifest, err
	}
	return manifest, nil
}

const environmentRestoreMarker = ".environment_restored.json"

// EnvironmentRestoredAt prevents a manually restored environment from being
// immediately reclaimed using its original completion timestamp.
func EnvironmentRestoredAt(path string) (time.Time, error) {
	var restored time.Time
	root, err := os.OpenRoot(path)
	if err != nil {
		return restored, err
	}
	defer root.Close()
	info, err := root.Lstat(environmentRestoreMarker)
	if errors.Is(err, os.ErrNotExist) {
		return restored, nil
	}
	if err != nil {
		return restored, err
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return restored, errors.New("invalid restoration marker")
	}
	data, err := root.ReadFile(environmentRestoreMarker)
	if err != nil {
		return restored, err
	}
	if err := json.Unmarshal(data, &restored); err != nil || restored.IsZero() {
		return restored, errors.New("invalid restoration timestamp")
	}
	return restored, nil
}

func extractEnvironmentArchive(ctx context.Context, archive, staging *os.Root) ([]archivedDirectoryMode, error) {
	file, err := archive.Open("environment.tar.gz")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	reader := tar.NewReader(archiveContextReader{ctx, compressed})
	type symbolicLink struct{ name, target string }
	links := []symbolicLink{}
	modes := []archivedDirectoryMode{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !validEnvironmentPayloadPath(header.Name) {
			return nil, errors.New("invalid restore path")
		}
		name := filepath.FromSlash(header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := staging.MkdirAll(name, 0700); err != nil {
				return nil, err
			}
			modes = append(modes, archivedDirectoryMode{name, os.FileMode(header.Mode) & 0777, header.ModTime})
		case tar.TypeSymlink:
			links = append(links, symbolicLink{name, header.Linkname})
		case tar.TypeReg:
			if err := staging.MkdirAll(filepath.Dir(name), 0700); err != nil {
				return nil, err
			}
			output, err := staging.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0777)
			if err != nil {
				return nil, err
			}
			_, copyErr := io.Copy(output, reader)
			if err := errors.Join(copyErr, output.Chmod(os.FileMode(header.Mode)&0777), output.Sync(), output.Close()); err != nil {
				return nil, err
			}
			if err := staging.Chtimes(name, header.ModTime, header.ModTime); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unsupported restore entry")
		}
	}
	for _, link := range links {
		if err := staging.Symlink(link.target, link.name); err != nil {
			return nil, err
		}
	}
	return modes, nil
}
