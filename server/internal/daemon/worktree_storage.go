package daemon

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

type worktreeStorageUsage struct {
	CodeBytes      int64  `json:"code_bytes"`
	OutputBytes    int64  `json:"output_bytes"`
	LogBytes       int64  `json:"log_bytes"`
	RuntimeBytes   int64  `json:"runtime_bytes"`
	OtherBytes     int64  `json:"other_bytes"`
	AllocatedBytes *int64 `json:"allocated_bytes"`
	SharedBytes    int64  `json:"shared_bytes"`
}

// The allocated subtotal attributes each inode once across the inventory.
// Filesystem clones may still share extents; this is not a free-space promise.
func scanWorktreeStorage(ctx context.Context, root string, allocatedFiles map[string]bool) (worktreeStorageUsage, error) {
	usage := worktreeStorageUsage{}
	allocated := int64(0)
	allocationKnown := true
	observedFiles := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
		switch first {
		case "workdir", "worktree":
			usage.CodeBytes += info.Size()
		case "output":
			usage.OutputBytes += info.Size()
		case "logbook", "logs":
			usage.LogBytes += info.Size()
		case "codex-home", "claude-home", "hermes-home":
			usage.RuntimeBytes += info.Size()
		default:
			usage.OtherBytes += info.Size()
		}
		identity, bytes, known := worktreeFileAllocation(info)
		if !known {
			allocationKnown = false
		} else if allocatedFiles[identity] || observedFiles[identity] {
			usage.SharedBytes += bytes
		} else {
			observedFiles[identity] = true
			allocated += bytes
		}
		return nil
	})
	if err != nil {
		return worktreeStorageUsage{}, err
	}
	if allocationKnown {
		usage.AllocatedBytes = &allocated
	}
	for identity := range observedFiles {
		allocatedFiles[identity] = true
	}
	return usage, nil
}
