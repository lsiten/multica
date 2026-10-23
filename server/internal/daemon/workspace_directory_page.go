package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type workspaceDirectoryEntry struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Directory bool   `json:"directory"`
}

type workspaceDirectoryPage struct {
	Path       string                    `json:"path"`
	Entries    []workspaceDirectoryEntry `json:"entries"`
	Recursive  bool                      `json:"recursive"`
	NextOffset int                       `json:"next_offset"`
	HasMore    bool                      `json:"has_more"`
	Truncated  bool                      `json:"truncated"`
}

// Only immediate children spend a page's budget. Descendants are fetched when
// their directory is opened, so one large subtree cannot hide its siblings.
func readWorkspaceDirectoryPage(ctx context.Context, root, selected string, offset, limit int) (workspaceDirectoryPage, error) {
	page := workspaceDirectoryPage{Entries: []workspaceDirectoryEntry{}, NextOffset: offset}
	if offset < 0 || limit < 0 || limit > 500 {
		return page, fmt.Errorf("invalid directory page")
	}
	if limit == 0 {
		limit = 100
	}
	entries, err := os.ReadDir(selected)
	if err != nil {
		return page, err
	}
	for i := offset; i < len(entries); i++ {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		entry := entries[i]
		page.NextOffset = i + 1
		if entry.Name() == ".git" || entry.Name() == ".multica" || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		relative, err := filepath.Rel(root, filepath.Join(selected, entry.Name()))
		if err != nil || !filepath.IsLocal(relative) {
			return page, fmt.Errorf("directory entry escapes root")
		}
		info, err := entry.Info()
		if err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, workspaceDirectoryEntry{Path: filepath.ToSlash(relative), Size: info.Size(), Directory: entry.IsDir()})
		if len(page.Entries) == limit {
			page.HasMore = i+1 < len(entries)
			break
		}
	}
	return page, nil
}
