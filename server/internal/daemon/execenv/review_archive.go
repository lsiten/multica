package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ReviewArchivePath(workspacesRoot, workspaceID, taskID string) string {
	hash := sha256.Sum256([]byte(workspaceID + "\x00" + taskID))
	return filepath.Join(workspacesRoot, ".local-mr-archive", hex.EncodeToString(hash[:]))
}

// ArchiveReviewDirectory preserves bindings and recovery receipts before task GC.
// It copies review metadata and diff receipts, not checkouts or runtime credentials.
func ArchiveReviewDirectory(workspacesRoot, taskRoot string) error {
	binding, err := ReadReviewDirectory(taskRoot)
	hasBinding := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(taskRoot)
	if err != nil {
		return err
	}
	hasReceipt := false
	for _, entry := range entries {
		if isReviewReceipt(entry) {
			hasReceipt = true
			break
		}
	}
	if !hasBinding && !hasReceipt {
		return nil
	}
	owner, err := ReadEnvRootOwner(taskRoot)
	if err != nil {
		return err
	}
	if owner.TaskID == "" || owner.WorkspaceID == "" || (hasBinding && (binding.TaskID != owner.TaskID || binding.WorkspaceID != owner.WorkspaceID)) {
		return errors.New("review binding does not match task owner")
	}
	destination := ReviewArchivePath(workspacesRoot, owner.WorkspaceID, owner.TaskID)
	workspace, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return err
	}
	defer workspace.Close()
	relative, err := filepath.Rel(workspacesRoot, destination)
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("invalid archive directory")
	}
	if err := workspace.MkdirAll(relative, 0o700); err != nil {
		return err
	}
	archive, err := workspace.OpenRoot(relative)
	if err != nil {
		return err
	}
	defer archive.Close()
	write := func(name string, data []byte) error {
		file, err := archive.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := file.Write(data); err != nil {
			return err
		}
		return file.Sync()
	}
	if hasBinding {
		data, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		if err := write(reviewDirectoryFile, data); err != nil {
			return err
		}
	}
	runtime, err := ReadReviewRuntime(taskRoot)
	if err == nil {
		if runtime.WorkspaceID != owner.WorkspaceID || runtime.TaskID != owner.TaskID {
			return errors.New("runtime binding does not match task owner")
		}
		data, err := json.Marshal(runtime)
		if err != nil {
			return err
		}
		if err := write(reviewRuntimeFile, data); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root, err := os.OpenRoot(taskRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, entry := range entries {
		name := entry.Name()
		if !isReviewReceipt(entry) {
			continue
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, (12<<20)+1))
		file.Close()
		if err != nil {
			return err
		}
		if len(data) > 12<<20 {
			return errors.New("review receipt too large")
		}
		if err := write(name, data); err != nil {
			return err
		}
	}
	return nil
}

func isReviewReceipt(entry os.DirEntry) bool {
	name := entry.Name()
	if entry.Type() != 0 || !strings.HasPrefix(name, ".local-review-") || !strings.HasSuffix(name, ".json") {
		return false
	}
	key := strings.TrimSuffix(strings.TrimPrefix(name, ".local-review-"), ".json")
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}
