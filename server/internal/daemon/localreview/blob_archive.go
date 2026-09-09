package localreview

import (
	"context"
	"io"
)

type VersionArchive struct {
	Source, Destination string
	IDs                 []string
}

// ArchiveVersions copies only manifests referenced by review records and their
// cached content. Scratch patches and never-submitted versions are not copied.
func ArchiveVersions(ctx context.Context, request VersionArchive) error {
	if len(request.IDs) == 0 {
		return nil
	}
	source, err := OpenBlobStore(request.Source, 64<<20)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := OpenBlobStore(request.Destination, 64<<20)
	if err != nil {
		return err
	}
	defer destination.Close()
	copied := map[string]bool{}
	versions := map[string]bool{}
	for _, id := range request.IDs {
		if versions[id] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		version, err := source.LoadVersion(ctx, id)
		if err != nil {
			return err
		}
		for _, file := range version.Files {
			for _, side := range []struct {
				blob   *BlobRef
				cached bool
			}{{file.Old, file.OldCached}, {file.New, file.NewCached}} {
				if !side.cached {
					continue
				}
				if side.blob == nil {
					return ErrInvalidReviewVersion
				}
				blob := *side.blob
				if copied[blob.ID] {
					continue
				}
				err := source.consume(ctx, blob, func(reader io.Reader) error {
					archived, err := destination.Put(ctx, reader)
					if err != nil {
						return err
					}
					if archived != blob {
						return ErrSnapshotContentChanged
					}
					return nil
				})
				if err != nil {
					return err
				}
				copied[blob.ID] = true
			}
		}
		archived, err := destination.SaveVersion(ctx, version)
		if err != nil {
			return err
		}
		if archived != id {
			return ErrSnapshotContentChanged
		}
		versions[id] = true
	}
	return nil
}

func RecordVersionIDs(record Record) []string {
	ids := []string{}
	if record.VersionID != "" {
		ids = append(ids, record.VersionID)
	}
	if record.PreparedRequest != nil && record.PreparedRequest.VersionID != "" {
		ids = append(ids, record.PreparedRequest.VersionID)
	}
	for _, event := range record.Events {
		if event.VersionID != "" {
			ids = append(ids, event.VersionID)
		}
	}
	return ids
}
