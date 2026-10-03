package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

type worktreeCacheCandidate struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type worktreeCacheResult struct {
	EnvironmentID string                   `json:"environment_id"`
	WorkspaceID   string                   `json:"workspace_id"`
	TaskID        string                   `json:"task_id"`
	Revision      string                   `json:"revision"`
	Candidates    []worktreeCacheCandidate `json:"candidates"`
	SizeBytes     int64                    `json:"size_bytes"`
	RemovedBytes  int64                    `json:"removed_bytes"`
	RemovedCount  int                      `json:"removed_count"`
	Reason        string                   `json:"reason"`
}

type worktreeCacheSelection struct {
	EnvironmentID string `json:"environment_id"`
	Revision      string `json:"revision"`
}

type worktreeResourceRequest struct {
	Action      string                   `json:"action"`
	WorkspaceID string                   `json:"workspace_id"`
	Selections  []worktreeCacheSelection `json:"selections"`
}

func (d *Daemon) managedEnvironmentID(path, workspaceID, taskID string) string {
	digest := sha256.Sum256([]byte(d.cfg.Profile + "\x00" + workspaceID + "\x00" + taskID + "\x00" + path))
	return hex.EncodeToString(digest[:])
}

// worktreeCacheOperation retains the environment and its deliverables. A
// nonempty revision requests deletion and must match a fresh scan under both
// the daemon reservation and the cross-process execution lock.
func (d *Daemon) worktreeCacheOperation(ctx context.Context, path, revision string) worktreeCacheResult {
	result := worktreeCacheResult{Candidates: []worktreeCacheCandidate{}}
	release, available := d.reserveEnvRootForGC(path)
	if !available {
		result.Reason = "active"
		return result
	}
	defer release()
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		result.Reason = "unowned"
		return result
	}
	result.WorkspaceID, result.TaskID = owner.WorkspaceID, owner.TaskID
	result.EnvironmentID = d.managedEnvironmentID(path, owner.WorkspaceID, owner.TaskID)
	unlock, err := d.lockGCTaskDirectory(path)
	if err != nil {
		result.Reason = "active"
		return result
	}
	defer unlock()
	rootInfo, err := os.Lstat(path)
	if err != nil || rootInfo.Mode()&linkedDirModes != 0 {
		result.Reason = "unowned"
		return result
	}
	if d.client == nil {
		result.Reason = "unavailable"
		return result
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	if !validScopedTaskStatus(ctx, owner, status) {
		result.Reason = "scope_changed"
		return result
	}
	if !isAgentTaskTerminal(status.Status) {
		result.Reason = "active"
		return result
	}
	if scope, ok := ctx.Value(environmentScopeContextKey{}).(environmentOperationScope); ok && scope.Automatic {
		if eligible, reason := d.automaticCacheEligible(ctx, path, owner, status); !eligible {
			result.Reason = reason
			return result
		}
	}
	if active, err := localreview.HasActiveReview(ctx, path, time.Now()); err != nil {
		result.Reason = "unavailable"
		return result
	} else if active {
		result.Reason = "review"
		return result
	}
	candidates, fingerprint, err := d.scanWorktreeCaches(ctx, path)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	result.Candidates, result.Revision = candidates, fingerprint
	for _, candidate := range candidates {
		result.SizeBytes += candidate.SizeBytes
	}
	if revision == "" {
		return result
	}
	if revision != fingerprint {
		result.Reason = "preview_changed"
		return result
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	defer root.Close()
	openedInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(rootInfo, openedInfo) {
		result.Reason = "unowned"
		return result
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			result.Reason = "cancelled"
			break
		}
		currentInfo, err := os.Lstat(path)
		if err != nil || !os.SameFile(rootInfo, currentInfo) || currentInfo.Mode()&linkedDirModes != 0 {
			result.Reason = "unowned"
			break
		}
		if err := root.RemoveAll(candidate.Path); err != nil {
			result.Reason = "removal_failed"
			break
		}
		result.RemovedCount++
		result.RemovedBytes += candidate.SizeBytes
	}
	return result
}

func (d *Daemon) scanWorktreeCaches(ctx context.Context, root string) ([]worktreeCacheCandidate, string, error) {
	candidates := []worktreeCacheCandidate{}
	fingerprint := sha256.New()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", err
	}
	owner, err := d.gcTaskDirOwner(root)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, "", err
	}
	fmt.Fprintf(fingerprint, "%s\x00%s\x00%s\n", d.managedEnvironmentID(root, owner.WorkspaceID, owner.TaskID), cacheFileIdentity(info), info.ModTime().String())
	add := func(path string) error {
		relative, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(relative) {
			return errors.New("cache path outside managed environment")
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if canonical != filepath.Join(canonicalRoot, relative) {
			return nil
		}
		var size int64
		treeHash := sha256.New()
		unsafe := false
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(current)
				if err != nil {
					unsafe = true
					return fs.SkipAll
				}
				relativeTarget, err := filepath.Rel(canonical, target)
				if err != nil || !filepath.IsLocal(relativeTarget) {
					unsafe = true
					return fs.SkipAll
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				fmt.Fprintf(treeHash, "%s\x00%s\x00%d\x00%s\n", current, target, info.ModTime().UnixNano(), cacheFileIdentity(info))
				return nil
			}
			if entry.Name() == ".git" || entry.Type()&linkedDirModes != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
				unsafe = true
				return fs.SkipAll
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if current == path && !info.IsDir() {
				unsafe = true
				return fs.SkipAll
			}
			if info.Mode().IsRegular() {
				size += info.Size()
			}
			entryPath, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			fmt.Fprintf(treeHash, "%s\x00%d\x00%d\x00%d\x00%s\n", entryPath, info.Size(), info.ModTime().UnixNano(), info.Mode(), cacheFileIdentity(info))
			return nil
		})
		if err != nil {
			return err
		}
		if unsafe {
			return nil
		}
		fmt.Fprintf(fingerprint, "%s\x00%x\n", relative, treeHash.Sum(nil))
		candidates = append(candidates, worktreeCacheCandidate{Path: filepath.ToSlash(relative), SizeBytes: size})
		return nil
	}
	for _, relative := range execenv.ManagedReclaimableArtifactSubpaths() {
		if err := add(filepath.Join(root, relative)); err != nil {
			return nil, "", err
		}
	}
	repositories, reason := inspectWorktreeRepositories(ctx, root, d.cfg.WorkspacesRoot)
	if reason == "unavailable" {
		return nil, "", errors.New("repository inspection unavailable")
	}
	// Limit manual cache reclamation to the built-in regenerable directory
	// types. Operator patterns alone do not prove that an output is disposable.
	matcher := newArtifactMatcher(DefaultGCArtifactPatterns, nil)
	for _, repository := range repositories {
		err := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&linkedDirModes != 0 || entry.Name() == ".git" {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.IsDir() || path == repository {
				return nil
			}
			if _, matched := matcher.matchDirectory(root, path, entry); !matched {
				return nil
			}
			relative, err := filepath.Rel(repository, path)
			if err != nil {
				return err
			}
			preserved, err := worktreeGit(ctx, repository, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			if preserved == "" {
				if err := add(path); err != nil {
					return err
				}
			}
			return fs.SkipDir
		})
		if err != nil {
			return nil, "", err
		}
	}
	return candidates, hex.EncodeToString(fingerprint.Sum(nil)), nil
}

func (d *Daemon) manageWorktreeResources(w http.ResponseWriter, r *http.Request) {
	var request worktreeResourceRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || (request.Action != "preview_cache" && request.Action != "clean_cache") || len(request.Selections) > 1000 || (request.Action == "clean_cache" && len(request.Selections) == 0) {
		http.Error(w, "invalid resource operation", http.StatusBadRequest)
		return
	}
	for _, selection := range request.Selections {
		if len(selection.EnvironmentID) != 64 || (request.Action == "clean_cache" && len(selection.Revision) != 64) {
			http.Error(w, "environment id and preview revision are required", http.StatusBadRequest)
			return
		}
	}
	report, err := ScanDiskUsage(d.cfg.WorkspacesRoot, d.cfg.GCArtifactPatterns)
	if err != nil {
		http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
		return
	}
	known := make(map[string]string)
	for _, environment := range report.Tasks {
		owner, err := d.gcTaskDirOwner(environment.Path)
		if err != nil || (request.WorkspaceID != "" && owner.WorkspaceID != request.WorkspaceID) {
			continue
		}
		known[d.managedEnvironmentID(environment.Path, owner.WorkspaceID, owner.TaskID)] = environment.Path
	}
	selections := request.Selections
	if len(selections) == 0 {
		for _, environment := range report.Tasks {
			owner, err := d.gcTaskDirOwner(environment.Path)
			if err == nil {
				id := d.managedEnvironmentID(environment.Path, owner.WorkspaceID, owner.TaskID)
				if _, ok := known[id]; ok {
					selections = append(selections, worktreeCacheSelection{EnvironmentID: id})
				}
			} else if request.WorkspaceID == "" {
				selections = append(selections, worktreeCacheSelection{EnvironmentID: d.managedEnvironmentID(environment.Path, environment.WorkspaceID, "")})
			}
		}
	}
	results := make([]worktreeCacheResult, 0, len(selections))
	seen := make(map[string]bool)
	for _, selection := range selections {
		if seen[selection.EnvironmentID] {
			continue
		}
		seen[selection.EnvironmentID] = true
		path, ok := known[selection.EnvironmentID]
		if !ok {
			results = append(results, worktreeCacheResult{EnvironmentID: selection.EnvironmentID, Reason: "unowned", Candidates: []worktreeCacheCandidate{}})
			continue
		}
		revision := ""
		if request.Action == "clean_cache" {
			revision = selection.Revision
		}
		result := d.worktreeCacheOperation(r.Context(), path, revision)
		result.EnvironmentID = selection.EnvironmentID
		results = append(results, result)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		d.logger.Debug("worktree resource response interrupted", "error", err)
	}
}
