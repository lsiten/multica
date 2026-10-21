package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type UploadCleanupOptions struct {
	Cutoff      time.Time
	Limit       int
	Apply       bool
	WorkspaceID pgtype.UUID
}

type UploadCleanupCandidate struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	Reason string `json:"reason"`
}

type UploadCleanupReport struct {
	Candidates     []UploadCleanupCandidate `json:"candidates"`
	CandidateBytes int64                    `json:"candidate_bytes"`
	Deleted        int                      `json:"deleted"`
	DeletedBytes   int64                    `json:"deleted_bytes"`
	Failed         int                      `json:"failed"`
}

// UploadCleaner owns physical uploads; it never removes task/chat history.
type UploadCleaner struct {
	Pool    *pgxpool.Pool
	Storage storage.Storage
}

// Sweep inventories disk before taking short database locks. All references
// are then read from the primary, while restores and new bindings are blocked.
// Failed physical deletes leave attachment rows intact for a later round.
func (s UploadCleaner) Sweep(ctx context.Context, options UploadCleanupOptions) (UploadCleanupReport, error) {
	report := UploadCleanupReport{Candidates: []UploadCleanupCandidate{}}
	if options.Limit < 1 || options.Cutoff.IsZero() {
		return report, fmt.Errorf("upload cleanup requires a cutoff and positive limit")
	}
	local, isLocal := s.Storage.(*storage.LocalStorage)
	objects := make(map[string]storage.LocalUploadObject)
	if isLocal {
		inventory, err := local.CleanupInventory(ctx, options.Cutoff)
		if err != nil {
			return report, err
		}
		for _, object := range inventory {
			objects[object.Key] = object
		}
	}
	// A busy database skips cleanup. Even on a slow object store, these locks
	// cannot hold application writers for an entire worker interval.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("begin upload cleanup: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'; SET LOCAL statement_timeout = '2s'"); err != nil {
		return report, fmt.Errorf("set cleanup budget: %w", err)
	}
	queries := db.New(tx)
	if err := queries.LockUploadCleanupReferences(ctx); err != nil {
		return report, fmt.Errorf("lock upload references: %w", err)
	}
	rows, err := queries.UploadCleanupInventory(ctx, db.UploadCleanupInventoryParams{
		Cutoff: pgtype.Timestamptz{Time: options.Cutoff, Valid: true}, Scope: options.WorkspaceID,
	})
	if err != nil {
		return report, fmt.Errorf("read upload references: %w", err)
	}
	refs := newUploadReferences()
	attachments := make(map[string][]db.UploadCleanupInventoryRow)
	for _, row := range rows {
		if row.AttachmentID.Valid {
			key := s.Storage.KeyFromURL(row.Url)
			attachments[key] = append(attachments[key], row)
		} else {
			refs.add(s.Storage, row.Reference)
		}
	}
	keys := make(map[string]bool)
	for key := range objects {
		keys[key] = true
	}
	for key := range attachments {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var deleteErrors []error
	attempts := 0
	for _, key := range ordered {
		if options.WorkspaceID.Valid && len(attachments[key]) == 0 && !strings.HasPrefix(key, "workspaces/"+util.UUIDToString(options.WorkspaceID)+"/") {
			continue
		}
		if refs.keys[key] {
			continue
		}
		if id := uploadReferenceID.FindString(path.Base(key)); id != "" && refs.ids[strings.ToLower(id)] {
			continue
		}
		bound := attachments[key]
		protected := false
		for _, row := range bound {
			if refs.ids[util.UUIDToString(row.AttachmentID)] {
				protected = true
			}
		}
		if protected {
			continue
		}
		object, onDisk := objects[key]
		if isLocal && onDisk && object.Recent {
			continue
		}
		if !onDisk {
			object.Key = key
		}
		reason := "unreferenced"
		if len(bound) > 0 {
			reason = bound[0].CleanupReason
		}
		candidate := UploadCleanupCandidate{Key: key, Bytes: object.Bytes, Reason: reason}
		if !isLocal && len(bound) > 0 {
			candidate.Bytes = bound[0].SizeBytes
		}
		report.Candidates = append(report.Candidates, candidate)
		report.CandidateBytes += candidate.Bytes
		if !options.Apply || attempts >= options.Limit {
			continue
		}
		attempts++
		if isLocal {
			err = local.DeleteCleanupObject(ctx, object)
		} else {
			err = s.Storage.DeleteObject(ctx, key)
		}
		if err != nil {
			report.Failed++
			deleteErrors = append(deleteErrors, fmt.Errorf("delete upload %s: %w", key, err))
			continue
		}
		for _, row := range bound {
			if err := queries.DeletePurgedUploadAttachment(ctx, db.DeletePurgedUploadAttachmentParams{
				ID: row.AttachmentID, WorkspaceID: row.WorkspaceID,
			}); err != nil {
				return report, fmt.Errorf("delete purged attachment: %w", err)
			}
		}
		report.Deleted++
		report.DeletedBytes += candidate.Bytes
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("commit upload cleanup: %w", err)
	}
	return report, errors.Join(deleteErrors...)
}
