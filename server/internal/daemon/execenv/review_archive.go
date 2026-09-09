package execenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

func ReviewArchivePath(workspacesRoot, workspaceID, taskID string) string {
	hash := sha256.Sum256([]byte(workspaceID + "\x00" + taskID))
	return filepath.Join(workspacesRoot, ".local-mr-archive", hex.EncodeToString(hash[:]))
}

// ArchiveReviewDirectory preserves bindings and recovery receipts before task GC.
// It copies review metadata and diff receipts, not checkouts or runtime credentials.
func ArchiveReviewDirectory(ctx context.Context, workspacesRoot, taskRoot string) error {
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
	if filepath.Dir(relative) != ".local-mr-archive" {
		return errors.New("invalid review archive parent")
	}
	archiveParent, err := openReviewArchiveChild(workspace, ".local-mr-archive")
	if err != nil {
		return err
	}
	defer archiveParent.Close()
	archive, err := openReviewArchiveChild(archiveParent, filepath.Base(relative))
	if err != nil {
		return err
	}
	defer archive.Close()
	write := func(name string, data []byte) error {
		return writeReviewArchiveFile(archive, name, data)
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
	type receipt struct {
		name   string
		digest [32]byte
	}
	receipts := []receipt{}
	versionIDs := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !isReviewReceipt(entry) {
			continue
		}
		data, err := readReviewArchiveReceipt(root, name)
		if err != nil {
			return err
		}
		var record localreview.Record
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		versionIDs = append(versionIDs, localreview.RecordVersionIDs(record)...)
		receipts = append(receipts, receipt{name, sha256.Sum256(data)})
	}
	if err := localreview.ArchiveVersions(ctx, localreview.VersionArchive{Source: taskRoot, Destination: destination, IDs: versionIDs}); err != nil {
		return err
	}
	for _, receipt := range receipts {
		data, err := readReviewArchiveReceipt(root, receipt.name)
		if err != nil {
			return err
		}
		if sha256.Sum256(data) != receipt.digest {
			return errors.New("review receipt changed during archival")
		}
		if err := write(receipt.name, data); err != nil {
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
