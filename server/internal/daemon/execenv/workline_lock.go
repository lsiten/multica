package execenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// LockWorklinePreparation serializes discovery and publication across processes.
// It is released before agent execution; live checkouts keep their execution claim.
func LockWorklinePreparation(ctx context.Context, workspacesRoot string, scope ManagedEnvProvenance) (func(), error) {
	scope.AgentName, scope.ManagedBy = "", ""
	if scope.CodeWorkspaceIssueID != "" {
		scope.IssueID = scope.CodeWorkspaceIssueID
		scope.AgentID, scope.SquadID, scope.ChatSessionID, scope.AutopilotID = "", "", "", ""
	}
	data, err := json.Marshal(scope)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	root, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.Mkdir(".workline-locks", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := root.Lstat(".workline-locks")
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, errors.New("workline lock directory unavailable")
	}
	directory, err := root.OpenRoot(".workline-locks")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	name := hex.EncodeToString(digest[:]) + ".lock"
	file, err := directory.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrNotExist) {
		// Concurrent O_CREAT opens on macOS may report ENOENT after the other
		// creator publishes the file. Open that existing inode without creating it.
		file, err = directory.OpenFile(name, os.O_RDWR, 0600)
	}
	if err != nil {
		return nil, fmt.Errorf("open workline lock: %w", err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		locked, err := lockFileExclusiveNonBlocking(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if locked {
			return func() { releaseLockFile(file) }, nil
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
