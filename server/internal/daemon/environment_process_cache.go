package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/repocache"
)

// environmentRepoCache exposes the existing caller interface without creating
// a second Cache or executing repository-mutation closures in the controller.
type environmentRepoCache struct{ daemon *Daemon }

func (c *environmentRepoCache) read(workspaceID, url string) (path, bare string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child, err := c.daemon.ensureEnvironmentProcess(ctx)
	if err != nil {
		return "", ""
	}
	raw, err := child.process.Client.Read(ctx, "cache.lookup", marshalRaw(struct {
		WorkspaceID string `json:"workspace_id"`
		URL         string `json:"url"`
	}{workspaceID, url}))
	if err != nil {
		return "", ""
	}
	var result struct {
		Path     string `json:"path"`
		BarePath string `json:"bare_path"`
	}
	if decodeEnvironmentPayload(raw, &result) != nil {
		return "", ""
	}
	return result.Path, result.BarePath
}
func (c *environmentRepoCache) Lookup(workspaceID, url string) string {
	path, _ := c.read(workspaceID, url)
	return path
}
func (c *environmentRepoCache) BarePath(workspaceID, url string) string {
	_, bare := c.read(workspaceID, url)
	return bare
}
func (c *environmentRepoCache) Sync(workspaceID string, repos []repocache.RepoInfo) error {
	return c.SyncContext(context.Background(), workspaceID, repos)
}
func (c *environmentRepoCache) SyncContext(ctx context.Context, workspaceID string, repos []repocache.RepoInfo) error {
	child, err := c.daemon.ensureEnvironmentProcess(ctx)
	if err != nil {
		return err
	}
	if err = child.sync(ctx); err != nil {
		return err
	}
	return child.mutation(ctx, "cache.sync", struct {
		WorkspaceID string               `json:"workspace_id"`
		Repos       []repocache.RepoInfo `json:"repos"`
	}{workspaceID, repos}, nil)
}
func (c *environmentRepoCache) CreateWorktree(input repocache.WorktreeParams) (*repocache.WorktreeResult, error) {
	return c.CreateWorktreeContext(context.Background(), input)
}
func (c *environmentRepoCache) CreateWorktreeContext(ctx context.Context, input repocache.WorktreeParams) (*repocache.WorktreeResult, error) {
	child, err := c.daemon.ensureEnvironmentProcess(ctx)
	if err != nil {
		return nil, err
	}
	if err = child.sync(ctx); err != nil {
		return nil, err
	}
	var result repocache.WorktreeResult
	if err = child.mutation(ctx, "cache.checkout", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (c *environmentRepoCache) WithRepoLock(_ string, _ func() error) error {
	return errors.New("repository mutation must execute inside environment service")
}
