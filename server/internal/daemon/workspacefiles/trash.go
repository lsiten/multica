package workspacefiles

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type TrashEntry = protocol.WorkspaceTrashEntry
type TrashPage = protocol.WorkspaceTrashPage

type TrashPageRequest struct{ Offset, Limit int }

// ListTrash pages the private operation journal, including interrupted deletes
// whose payload was preserved. Reading an untouched directory creates no metadata.
func ListTrash(ctx context.Context, scope Scope, request TrashPageRequest) (TrashPage, error) {
	root := scope.Storage
	page := TrashPage{Kind: "workspace_trash", Entries: []TrashEntry{}, NextOffset: request.Offset}
	if request.Offset < 0 || request.Limit < 1 || request.Limit > 100 {
		return page, protocol.ErrInvalidWorkspaceFileOperation
	}
	if err := checkParents(root, recordsPath+"/receipt"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return page, nil
		}
		return page, err
	}
	records, err := root.OpenRoot(recordsPath)
	if err != nil {
		return page, err
	}
	defer records.Close()
	directory, err := records.Open(".")
	if err != nil {
		return page, err
	}
	defer directory.Close()
	for remaining := request.Offset; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		skipped, err := directory.ReadDir(min(remaining, 256))
		remaining -= len(skipped)
		if errors.Is(err, io.EOF) {
			return page, nil
		}
		if err != nil {
			return page, err
		}
	}
	entries, err := directory.ReadDir(request.Limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return page, err
	}
	page.HasMore = len(entries) > request.Limit
	for _, entry := range entries[:min(len(entries), request.Limit)] {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		page.NextOffset++
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".json")
		receipt, _, err := loadReceipt(records, key)
		if err != nil {
			return page, err
		}
		if receipt.Request.Operation.Kind != protocol.WorkspaceTrash || (receipt.State != "completed" && receipt.State != "prepared") {
			continue
		}
		info, err := regularEntry(records, key+".payload")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return page, err
		}
		recorded, err := records.Stat(key + ".json")
		if err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, TrashEntry{RecoveryID: receipt.Request.CommandID, Path: receipt.Request.Operation.Path, Directory: info.IsDir(), DeletedAt: recorded.ModTime().UTC().Format(time.RFC3339Nano), ActorID: receipt.Request.ActorID})
	}
	return page, nil
}
