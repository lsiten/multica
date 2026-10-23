package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

type pagedReviewContext struct {
	request    worktreeReviewRequest
	path, root string
}
type pagedReviewManifest struct {
	VersionID string                      `json:"version_id"`
	Header    localreview.VersionHeader   `json:"header"`
	Page      localreview.VersionFilePage `json:"page"`
	Review    localreview.RecordView      `json:"review"`
}
type pagedReviewFile struct {
	VersionID string                 `json:"version_id"`
	Path      string                 `json:"path"`
	Preview   string                 `json:"preview"`
	Reason    string                 `json:"reason,omitempty"`
	Page      *localreview.PatchPage `json:"page,omitempty"`
}

func isPagedReviewRead(action string) bool {
	switch action {
	case "index", "repositories", "manifest", "files", "file", "context", "content", "workspace_files", "file_content", "commits", "lease":
		return true
	default:
		return false
	}
}
func isReadReviewAction(action string) bool {
	return action == "" || action == "read" || action == "branches" || isPagedReviewRead(action)
}

// pagedReviewRead is called only after the existing task/directory ownership gate.
func (d *Daemon) pagedReviewRead(w http.ResponseWriter, r *http.Request, scope pagedReviewContext) {
	if scope.request.Action == "index" {
		d.localIndexRead(w, r, scope)
		return
	}
	if scope.request.Action == "workspace_files" || scope.request.Action == "file_content" {
		d.workspaceFileRead(w, r, scope)
		return
	}
	request := scope.request
	maxLimit := 500
	if request.Action == "content" {
		maxLimit = 64 << 10
	}
	if request.Offset < 0 || request.Limit < 0 || request.Limit > maxLimit || len(request.VersionID) > 64 || len(request.FilePath) > 4096 {
		http.Error(w, "invalid review page request", http.StatusBadRequest)
		return
	}
	if request.Limit == 0 {
		request.Limit = 100
	}
	if request.Action == "repositories" {
		repositories, err := localreview.Repositories(r.Context(), scope.path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		for i, path := range repositories {
			rel, err := filepath.Rel(scope.path, path)
			if err != nil || !filepath.IsLocal(rel) {
				http.Error(w, "repository discovery escaped task directory", http.StatusForbidden)
				return
			}
			repositories[i] = filepath.Join(request.Path, rel)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(struct {
			Repositories []string `json:"repositories"`
		}{repositories}); err != nil {
			d.logger.Debug("review repository response interrupted", "error", err)
		}
		return
	}
	store, err := localreview.OpenBlobStore(scope.root, 64<<20)
	if err != nil {
		http.Error(w, "cannot open local review cache", http.StatusConflict)
		return
	}
	defer store.Close()
	releaseRead, err := store.BeginRead(r.Context(), time.Now())
	if err != nil {
		http.Error(w, "cannot protect active review", http.StatusConflict)
		return
	}
	defer releaseRead()
	var version localreview.ReviewVersion
	id := request.VersionID
	if request.Action == "manifest" && id == "" {
		d.maintainReviewCache(r.Context(), store, scope.root, true)
		if binding, bindingErr := execenv.ReadReviewDirectory(scope.root); bindingErr == nil && binding.SourcePath == request.Path && binding.Commit != "" {
			version, err = store.CaptureCommitted(r.Context(), localreview.CommittedRequest{Path: scope.path, Branch: binding.Branch, Head: binding.Commit, Target: request.Target})
		} else {
			version, err = store.CaptureWorking(r.Context(), localreview.WorkingVersionRequest{Path: scope.path, Target: request.Target})
		}
		if err == nil {
			id, err = store.SaveVersion(r.Context(), version)
		}
	} else {
		version, err = store.LoadVersion(r.Context(), id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if version.Header.Repository != scope.path || version.Header.Target != request.Target {
		http.Error(w, "review version belongs to another repository or target", http.StatusForbidden)
		return
	}
	accessedAt := time.Now()
	if err := store.TouchVersion(r.Context(), id, accessedAt); err != nil {
		http.Error(w, "review version expired; refresh the review", http.StatusConflict)
		return
	}
	if request.Action == "lease" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(struct {
			VersionID string `json:"version_id"`
			ExpiresAt string `json:"expires_at"`
		}{id, accessedAt.Add(localreview.ReviewLeaseTTL).UTC().Format(time.RFC3339Nano)}); err != nil {
			d.logger.Debug("review lease response interrupted", "error", err)
		}
		return
	}
	d.maintainReviewCache(r.Context(), store, scope.root, false)
	if request.Action == "file" || request.Action == "context" || request.Action == "content" {
		index := slices.IndexFunc(version.Files, func(file localreview.VersionFile) bool { return file.Path == request.FilePath })
		if index < 0 {
			http.Error(w, "file is not part of this review version", http.StatusNotFound)
			return
		}
		file := version.Files[index]
		if request.Action == "context" {
			if file.New == nil || file.Preview != "text" {
				http.Error(w, "context is unavailable for this file", http.StatusConflict)
				return
			}
			page, err := store.ContextPage(r.Context(), localreview.CachedPatchPage{Blob: *file.New, Page: localreview.PatchPageRequest{Offset: int64(request.Offset), Limit: request.Limit}})
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if err := json.NewEncoder(w).Encode(pagedReviewFile{VersionID: id, Path: file.Path, Preview: "text", Page: &page}); err != nil {
				d.logger.Debug("review context response interrupted", "error", err)
			}
			return
		}
		if request.Action == "content" {
			if request.Side == "" {
				request.Side = "new"
			}
			if request.Side != "old" && request.Side != "new" {
				http.Error(w, "invalid file side", http.StatusBadRequest)
				return
			}
			content, err := store.VersionContent(r.Context(), version, localreview.VersionContentRequest{FilePath: request.FilePath, Side: request.Side, Offset: int64(request.Offset), Limit: request.Limit})
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			response := struct {
				VersionID string                  `json:"version_id"`
				Path      string                  `json:"path"`
				Side      string                  `json:"side"`
				Content   localreview.ContentPage `json:"content"`
			}{id, file.Path, request.Side, content}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				d.logger.Debug("review content response interrupted", "error", err)
			}
			return
		}
		response := pagedReviewFile{VersionID: id, Path: file.Path, Preview: file.Preview}
		if file.Preview == "text" {
			patch, patchErr := store.FilePatch(r.Context(), file)
			if errors.Is(patchErr, localreview.ErrSnapshotBlobTooLarge) || errors.Is(patchErr, localreview.ErrSnapshotCacheFull) {
				response.Preview, response.Reason = "too_large", patchErr.Error()
				if errors.Is(patchErr, localreview.ErrSnapshotCacheFull) {
					response.Preview = "uncached"
				}
			} else if patchErr != nil {
				http.Error(w, patchErr.Error(), http.StatusConflict)
				return
			} else {
				page, pageErr := store.PatchPage(r.Context(), localreview.CachedPatchPage{Blob: patch, Page: localreview.PatchPageRequest{Offset: int64(request.Offset), Limit: request.Limit}})
				if errors.Is(pageErr, localreview.ErrPatchLineTooLarge) {
					response.Preview, response.Reason = "too_large", pageErr.Error()
				} else if pageErr != nil {
					http.Error(w, pageErr.Error(), http.StatusConflict)
					return
				} else {
					response.Page = &page
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			d.logger.Debug("local review file response interrupted", "error", err)
		}
		return
	}
	if request.Action == "commits" {
		page, err := version.CommitPage(r.Context(), localreview.FilePageRequest{Offset: request.Offset, Limit: request.Limit})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(struct {
			VersionID string `json:"version_id"`
			localreview.VersionCommitPage
		}{id, page}); err != nil {
			d.logger.Debug("review commit response interrupted", "error", err)
		}
		return
	}
	page, err := version.FilePage(localreview.FilePageRequest{Offset: request.Offset, Limit: request.Limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := localreview.RecordKey(localreview.Snapshot{Path: scope.path, Target: request.Target})
	record, err := localreview.LoadRecord(scope.root, key)
	if err != nil {
		http.Error(w, "cannot read review record", http.StatusConflict)
		return
	}
	if record.SnapshotID != id {
		record = localreview.Record{SnapshotID: id, State: "draft", Events: record.Events}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	response := pagedReviewManifest{VersionID: id, Header: version.Header, Page: page, Review: record.View()}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		d.logger.Debug("local review manifest response interrupted", "error", err)
	}
}

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
			Path      string  `json:"path"`
			Entries   []entry `json:"entries"`
			Truncated bool    `json:"truncated"`
		}{Path: scope.request.Path, Entries: entries, Truncated: truncated})
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
