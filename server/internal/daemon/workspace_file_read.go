package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const chatWorkspaceFileLimit = 5000

func safeWorkspaceRelativePath(raw string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "." || cleaned == "" {
		return ".", nil
	}
	if filepath.IsAbs(cleaned) || !filepath.IsLocal(cleaned) {
		return "", errors.New("workspace file path escapes task directory")
	}
	return cleaned, nil
}

func (d *Daemon) workspaceFileRead(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	relative, err := safeWorkspaceRelativePath(scope.request.RelativePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	root, err := filepath.EvalSymlinks(scope.path)
	if err != nil {
		http.Error(w, "workspace directory unavailable", http.StatusConflict)
		return
	}
	selected := filepath.Join(root, relative)
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		http.Error(w, "workspace file not found", http.StatusNotFound)
		return
	}
	inside, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(inside) {
		http.Error(w, "workspace file escapes task directory", http.StatusForbidden)
		return
	}
	if scope.request.Action == "workspace_files" {
		if scope.request.Recursive != nil && !*scope.request.Recursive {
			page, err := readWorkspaceDirectoryPage(r.Context(), root, resolved, scope.request.Offset, scope.request.Limit)
			if err != nil {
				http.Error(w, "cannot list workspace directory", http.StatusConflict)
				return
			}
			page.Path = scope.request.Path
			page.FileOperationsSupported = scope.request.TaskID != "" && (d.environmentServiceOwner || d.environmentProcessMode())
			page.FileOperationIdentitySupported = page.FileOperationsSupported
			page.FileEditIdentitySupported = page.FileOperationsSupported
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		type entry struct {
			Path      string `json:"path"`
			Size      int64  `json:"size"`
			Directory bool   `json:"directory"`
		}
		entries := make([]entry, 0, 128)
		truncated := false
		walkErr := filepath.WalkDir(resolved, func(path string, dirEntry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if len(entries) >= chatWorkspaceFileLimit {
				truncated = true
				return filepath.SkipAll
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || !filepath.IsLocal(rel) {
				return errors.New("workspace file path escapes task directory")
			}
			if dirEntry.IsDir() && (dirEntry.Name() == ".git" || dirEntry.Name() == ".multica") {
				if path != resolved {
					return filepath.SkipDir
				}
			}
			if path == resolved {
				return nil
			}
			if dirEntry.Type()&os.ModeSymlink != 0 {
				if dirEntry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			info, infoErr := dirEntry.Info()
			if infoErr != nil {
				return infoErr
			}
			entries = append(entries, entry{Path: filepath.ToSlash(rel), Size: info.Size(), Directory: dirEntry.IsDir()})
			return nil
		})
		if walkErr != nil {
			http.Error(w, "cannot list workspace files", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			FileOperationsSupported        bool    `json:"file_operations_supported"`
			FileOperationIdentitySupported bool    `json:"file_operation_identity_supported"`
			FileEditIdentitySupported      bool    `json:"file_edit_identity_supported"`
			Path                           string  `json:"path"`
			Entries                        []entry `json:"entries"`
			Truncated                      bool    `json:"truncated"`
		}{FileOperationsSupported: scope.request.TaskID != "" && (d.environmentServiceOwner || d.environmentProcessMode()), FileOperationIdentitySupported: scope.request.TaskID != "" && (d.environmentServiceOwner || d.environmentProcessMode()), FileEditIdentitySupported: scope.request.TaskID != "" && (d.environmentServiceOwner || d.environmentProcessMode()), Path: scope.request.Path, Entries: entries, Truncated: truncated})
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "workspace file is not a regular file", http.StatusBadRequest)
		return
	}
	if scope.request.Offset < 0 || scope.request.Limit <= 0 || scope.request.Limit > 256<<10 {
		http.Error(w, "invalid workspace file page", http.StatusBadRequest)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.Error(w, "cannot open workspace file", http.StatusConflict)
		return
	}
	defer file.Close()
	if _, err := file.Seek(int64(scope.request.Offset), io.SeekStart); err != nil {
		http.Error(w, "cannot seek workspace file", http.StatusConflict)
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(scope.request.Limit)+1))
	if err != nil {
		http.Error(w, "cannot read workspace file", http.StatusConflict)
		return
	}
	if len(content) > scope.request.Limit {
		content = content[:scope.request.Limit]
	}
	// A read boundary can split a multibyte character; back up over the trailing
	// partial rune so the page stays decodable. Anything still invalid is a
	// non-text file and is rejected.
	if !utf8.ValidString(string(content)) {
		for i := len(content); i > len(content)-4 && i > 0; i-- {
			if utf8.ValidString(string(content[:i])) {
				content = content[:i]
				break
			}
		}
		if !utf8.ValidString(string(content)) {
			http.Error(w, "workspace file is not UTF-8 text", http.StatusUnsupportedMediaType)
			return
		}
	}
	text := string(content)
	nextOffset := scope.request.Offset + len(content)
	hasMore := int64(nextOffset) < info.Size()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Path    string `json:"path"`
		Content struct {
			Text       string `json:"text"`
			Encoding   string `json:"encoding"`
			Offset     int    `json:"offset"`
			NextOffset int    `json:"next_offset"`
			Size       int64  `json:"size"`
			HasMore    bool   `json:"has_more"`
		} `json:"content"`
	}{Path: filepath.ToSlash(relative), Content: struct {
		Text       string `json:"text"`
		Encoding   string `json:"encoding"`
		Offset     int    `json:"offset"`
		NextOffset int    `json:"next_offset"`
		Size       int64  `json:"size"`
		HasMore    bool   `json:"has_more"`
	}{Text: text, Encoding: "utf8", Offset: scope.request.Offset, NextOffset: nextOffset, Size: info.Size(), HasMore: hasMore}})
}
