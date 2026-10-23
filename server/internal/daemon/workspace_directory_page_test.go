package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDirectoryPagesDoNotSpendBudgetOnDescendants(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := t.TempDir()
	deep := filepath.Join(root, ".audit")
	if err := os.Mkdir(deep, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < chatWorkspaceFileLimit+1; i++ {
		p := filepath.Join(deep, fmt.Sprintf("%05d.txt", i))
		if err := os.WriteFile(p, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"00-项目管理", "01-品牌与个人IP", "空目录"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	recursive := false
	offset := 0
	seen := map[string]bool{}
	for {
		result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", Path: root, Action: "workspace_files", Recursive: &recursive, Offset: offset, Limit: 2})
		if result.Error != "" {
			t.Fatal(result.Error)
		}
		var page struct {
			Entries []struct {
				Path      string `json:"path"`
				Directory bool   `json:"directory"`
			} `json:"entries"`
			NextOffset int  `json:"next_offset"`
			HasMore    bool `json:"has_more"`
		}
		if err := json.Unmarshal(result.Page, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 2 {
			t.Fatalf("shallow page traversed descendants: %d entries", len(page.Entries))
		}
		for _, entry := range page.Entries {
			seen[entry.Path] = true
			if !entry.Directory {
				t.Fatalf("descendant leaked: %s", entry.Path)
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = page.NextOffset
	}
	for _, name := range []string{".audit", "00-项目管理", "01-品牌与个人IP", "空目录"} {
		if !seen[name] {
			t.Errorf("missing directory %s", name)
		}
	}
}

func TestWorkspaceDirectoryPagingSkipsPrivateAndEscapingEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".multica"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	page, err := readWorkspaceDirectoryPage(t.Context(), root, root, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Path != "空目录" || page.HasMore || page.NextOffset != 4 {
		t.Fatalf("unexpected visible page: %+v", page)
	}
}
