package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workspaceFileDirectory struct {
	workspaceID       pgtype.UUID
	key               string
	hostKey           string
	physicalKey       string
	root              string
	runtime           db.AgentRuntime
	readRevision      *int64
	expectedFileID    string
	identityAmbiguous bool
	binding           DocumentCommentResource
}

type workspaceFileObservation struct {
	Path      string `json:"relative_path"`
	PathKey   string `json:"path_key"`
	Directory bool   `json:"directory"`
}

var errInvalidWorkspaceFileObservation = errors.New("invalid workspace file identity response")

func workspaceFileDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// Daemon-backed providers share a filesystem. A hosted runtime without a
// daemon has its own host identity; task generations never enter this key.
func newWorkspaceFileDirectory(runtime db.AgentRuntime, binding DocumentCommentResource) workspaceFileDirectory {
	host := "runtime:" + uuidToString(runtime.ID)
	if runtime.DaemonID.Valid && runtime.DaemonID.String != "" {
		host = "daemon:" + runtime.DaemonID.String
	}
	root := path.Clean(strings.ReplaceAll(binding.WorkDir, "\\", "/"))
	key := workspaceFileDigest(strings.Join([]string{uuidToString(runtime.WorkspaceID), uuidToString(runtime.OwnerID), host, binding.OwnerKind, binding.OwnerID, root}, "\x00"))
	hostKey := workspaceFileDigest(strings.Join([]string{uuidToString(runtime.WorkspaceID), uuidToString(runtime.OwnerID), host}, "\x00"))
	return workspaceFileDirectory{workspaceID: runtime.WorkspaceID, key: key, hostKey: hostKey, physicalKey: workspaceFileDigest(hostKey + "\x00" + root), root: root, runtime: runtime, binding: binding}
}

func (h *Handler) observeWorkspaceFiles(ctx context.Context, directory workspaceFileDirectory, entries []workspaceFileObservation, observed bool) (map[string]db.WorkspaceFileResource, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin workspace file observation: %w", err)
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	host, err := lockWorkspaceFileHost(ctx, q, directory)
	if err != nil {
		return nil, err
	}
	if err := checkWorkspaceFileRead(ctx, q, directory, host.Revision); err != nil {
		return nil, err
	}
	resources, err := observeWorkspaceFilesInTransaction(ctx, q, directory, entries, observed)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit workspace file observations: %w", err)
	}
	return resources, nil
}
