package localreview

import (
	"context"
	"errors"
	"io"
	"slices"
)

type IndexSelection struct {
	Version VersionSelection
	IndexID string
	Paths   []string
	Unstage bool
}

// ChangeStaging writes only selected paths from a HEAD-based working snapshot.
// The caller must authorize ownership and hold the runtime repository locks.
func (s *BlobStore) ChangeStaging(ctx context.Context, selection IndexSelection) (err error) {
	return s.ChangeStagingPrepared(ctx, selection, nil)
}

func (s *BlobStore) ChangeStagingPrepared(ctx context.Context, selection IndexSelection, prepare func(string) error) (err error) {
	if !validBlobID(selection.IndexID) || len(selection.Paths) == 0 || len(selection.Paths) > 1000 {
		return ErrInvalidIndexStatus
	}
	tx, err := openIndexTransaction(ctx, selection.Version.Path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()
	if tx.original != selection.IndexID {
		return ErrIndexStateChanged
	}
	version, err := s.LoadVersion(ctx, selection.Version.ID)
	if err != nil {
		return err
	}
	if version.Header.Committed || version.Header.Repository != selection.Version.Path || version.Header.Target != tx.branch || selection.Version.Target != tx.branch || version.Header.Head != tx.head {
		return ErrIndexStateChanged
	}
	if !selection.Unstage {
		if _, err := s.VerifyVersion(ctx, selection.Version); err != nil {
			return err
		}
	}
	status, err := ReadIndexStatus(ctx, tx.repository)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, name := range selection.Paths {
		if !validVersionPath(name) || seen[name] {
			return ErrInvalidIndexStatus
		}
		seen[name] = true
		position := slices.IndexFunc(status.Files, func(file IndexFile) bool { return file.Path == name })
		if position < 0 {
			return ErrIndexStateChanged
		}
		file := status.Files[position]
		if file.Conflicted || file.Unsupported || (selection.Unstage && !file.Staged) || (!selection.Unstage && !file.Unstaged) {
			return ErrInvalidIndexStatus
		}
		if selection.Unstage {
			paths := []string{name}
			if file.OldPath != "" {
				paths = append(paths, file.OldPath)
			}
			if _, err := tx.git(ctx, nil, append([]string{"restore", "--staged", "--source=HEAD", "--"}, paths...)...); err != nil {
				return err
			}
			continue
		}
		position = slices.IndexFunc(version.Files, func(file VersionFile) bool { return file.Path == name })
		if position < 0 {
			// A partially staged edit can have working bytes equal to HEAD.
			if _, err := tx.git(ctx, nil, "restore", "--staged", "--source=HEAD", "--", name); err != nil {
				return err
			}
			continue
		}
		if err := s.stageCapturedFile(ctx, tx, version.Files[position]); err != nil {
			return err
		}
	}
	if prepare != nil {
		indexID, err := copyIndex(ctx, tx.root, tx.scratch, io.Discard)
		if err != nil {
			return err
		}
		if err := prepare(indexID); err != nil {
			return err
		}
	}
	return tx.publish(ctx)
}

func (s *BlobStore) stageCapturedFile(ctx context.Context, tx *indexTransaction, file VersionFile) error {
	if file.Status == "deleted" {
		_, err := tx.git(ctx, nil, "update-index", "--force-remove", "--", file.Path)
		return err
	}
	if file.New == nil || (file.NewMode != "100644" && file.NewMode != "100755" && file.NewMode != "120000") {
		return ErrFilePreviewUnavailable
	}
	var oid string
	err := s.stagingContent(ctx, tx.repository, file, func(reader io.Reader) error {
		var err error
		oid, err = tx.git(ctx, reader, "hash-object", "-w", "--stdin", "--no-filters")
		return err
	})
	if err != nil {
		return err
	}
	if !validGitObjectID(oid) {
		return ErrInvalidSnapshotBlob
	}
	_, err = tx.git(ctx, nil, "update-index", "--add", "--cacheinfo", file.NewMode, oid, file.Path)
	return err
}
