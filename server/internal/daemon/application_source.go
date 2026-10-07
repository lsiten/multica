package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/processtree"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) applicationDirectory(command protocol.ApplicationControlCommand) (string, error) {
	for _, id := range []string{d.cfg.DaemonID, command.WorkspaceID, command.RuntimeID, command.ApplicationID, command.InstanceID} {
		if _, err := util.ParseUUID(id); err != nil {
			return "", errors.New("invalid application ownership identity")
		}
	}
	if !filepath.IsAbs(d.cfg.WorkspacesRoot) {
		return "", errors.New("application storage root must be absolute")
	}
	root := filepath.Join(d.cfg.WorkspacesRoot, ".applications", d.cfg.DaemonID, command.WorkspaceID, command.InstanceID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	parent, err := os.Lstat(root)
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("application storage directory is not an owned directory")
	}
	return root, nil
}

func (d *Daemon) applicationSource(ctx context.Context, command protocol.ApplicationControlCommand) (root, workDir, version string, dirty bool, returnErr error) {
	if err := command.Config.Validate("service"); err != nil {
		return "", "", "", false, err
	}
	if command.Config.Mode != "managed" {
		return "", "", "", false, errors.New("external services have no managed source")
	}
	storage, err := d.applicationDirectory(command)
	if err != nil {
		return "", "", "", false, err
	}
	var repoURL, ref string
	switch command.ResourceType {
	case "local_directory":
		var resource localDirectoryRef
		if err = json.Unmarshal(command.ResourceRef, &resource); err != nil {
			return "", "", "", false, err
		}
		if resource.DaemonID != d.cfg.DaemonID {
			return "", "", "", false, errors.New("local application resource belongs to another machine")
		}
		root, err = normalizeLocalPath(resource.LocalPath)
		if err != nil {
			return "", "", "", false, err
		}
		if err = validateLocalPath(root); err != nil {
			return "", "", "", false, err
		}
		if command.Config.Ref != "" {
			repoURL = root
			ref = command.Config.Ref
		}
	case "github_repo":
		var resource struct {
			URL string `json:"url"`
			Ref string `json:"ref"`
		}
		if err = json.Unmarshal(command.ResourceRef, &resource); err != nil {
			return "", "", "", false, err
		}
		if resource.URL == "" {
			return "", "", "", false, errors.New("application repository URL is missing")
		}
		repoURL, ref = resource.URL, resource.Ref
		if command.Config.Ref != "" {
			ref = command.Config.Ref
		}
	default:
		return "", "", "", false, errors.New("application requires a supported project resource")
	}
	if repoURL != "" {
		if d.repoCache == nil {
			return "", "", "", false, errors.New("application repository cache is unavailable")
		}
		prepareCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err = d.repoCache.SyncContext(prepareCtx, command.WorkspaceID, []repocache.RepoInfo{{URL: repoURL}}); err != nil {
			return "", "", "", false, err
		}
		checkout, checkoutErr := d.repoCache.CreateWorktreeContext(prepareCtx, repocache.WorktreeParams{WorkspaceID: command.WorkspaceID, RepoURL: repoURL, Ref: ref, WorkDir: filepath.Join(storage, "revisions", strconv.FormatInt(command.Revision, 10)), AgentName: "application", TaskID: command.InstanceID, IsolatedGitMetadata: true, LockWaitTimeout: 30 * time.Second})
		if checkoutErr != nil {
			return "", "", "", false, checkoutErr
		}
		root = checkout.Path
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", false, err
	}
	workDir, err = filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(command.Config.WorkDir)))
	if err != nil {
		return "", "", "", false, fmt.Errorf("application working directory does not exist: %w", err)
	}
	relative, err := filepath.Rel(root, workDir)
	if err != nil || !filepath.IsLocal(relative) {
		return "", "", "", false, errors.New("application working directory escaped its resource")
	}
	info, err := os.Stat(workDir)
	if err != nil || !info.IsDir() {
		return "", "", "", false, errors.New("application working directory is not a directory")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	git := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	output, probeErr := processtree.Output(probeCtx, git, time.Second)
	if probeErr == nil {
		version = strings.TrimSpace(string(output))
		git = exec.Command("git", "-C", root, "status", "--porcelain")
		output, probeErr = processtree.Output(probeCtx, git, time.Second)
		if probeErr != nil {
			return "", "", "", false, fmt.Errorf("inspect application code changes: %w", probeErr)
		}
		dirty = len(strings.TrimSpace(string(output))) > 0
	} else if repoURL != "" {
		return "", "", "", false, fmt.Errorf("inspect application code version: %w", probeErr)
	}
	return root, workDir, version, dirty, nil
}
