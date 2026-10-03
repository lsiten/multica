package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentCapacityGroup struct {
	scope environmentOperationScope
	rows  []TaskDiskUsage
	bytes int64
}

func (d *Daemon) scanEnvironmentCapacity(ctx context.Context) {
	if !d.cfg.GCEnabled || !d.cfg.EnvironmentArchiveEnabled || d.cfg.KeepEnvAfterTask {
		return
	}
	roots, err := d.environmentRootPaths(ctx)
	if err != nil {
		return
	}
	groups := map[string]*environmentCapacityGroup{}
	for _, path := range roots {
		if ctx.Err() != nil {
			return
		}
		owner, err := d.gcTaskDirOwner(path)
		if err != nil {
			continue
		}
		status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
		if err != nil || status.WorkspaceID != owner.WorkspaceID {
			continue
		}
		usage, err := scanWorktreeStorage(ctx, path, map[string]bool{})
		if err != nil {
			continue
		}
		row := TaskDiskUsage{Path: path, SizeBytes: usage.CodeBytes + usage.OutputBytes + usage.LogBytes + usage.RuntimeBytes + usage.OtherBytes}
		scope := environmentOperationScope{WorkspaceID: owner.WorkspaceID, RuntimeID: status.RuntimeID}
		key := d.environmentOperationKey(scope, "policy")
		group := groups[key]
		if group == nil {
			group = &environmentCapacityGroup{scope: scope}
			groups[key] = group
		}
		group.bytes += row.SizeBytes
		if isAgentTaskTerminal(status.Status) && !d.isActiveEnvRoot(row.Path) {
			group.rows = append(group.rows, row)
		}
	}
	free, err := environmentFreeBytes(d.cfg.WorkspacesRoot)
	if err != nil {
		free = nil
	}
	for key, group := range groups {
		policy, err := d.loadEnvironmentPolicy(group.scope)
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		pressure := environmentUnderPressure(policy, len(group.rows), group.bytes, free)
		d.environmentPolicyMu.Lock()
		if d.environmentPolicyScans == nil {
			d.environmentPolicyScans = make(map[string]protocol.EnvironmentPolicyStatus)
		}
		d.environmentPolicyScans[key] = protocol.EnvironmentPolicyStatus{LastScanAt: &now, IdleEnvironments: len(group.rows), DirectoryBytes: group.bytes, UnderPressure: pressure}
		d.environmentPolicyMu.Unlock()
		if !policy.Enabled {
			continue
		}
		sort.Slice(group.rows, func(i, j int) bool { return group.rows[i].Path < group.rows[j].Path })
		cacheHours := policy.CacheAfterHours
		if pressure && policy.PressureCacheAfterHours < cacheHours {
			cacheHours = policy.PressureCacheAfterHours
		}
		if cacheHours <= 0 {
			continue
		}
		selections := []protocol.EnvironmentSelection{}
		for _, row := range group.rows {
			if ctx.Err() != nil {
				return
			}
			meta, err := execenv.ReadGCMeta(row.Path)
			if err != nil || meta.CompletedAt.IsZero() || time.Since(meta.CompletedAt) < time.Duration(cacheHours)*time.Hour {
				continue
			}
			preview := d.worktreeCacheOperation(withEnvironmentScope(ctx, group.scope), row.Path, "")
			if preview.Reason != "" || len(preview.Candidates) == 0 {
				continue
			}
			selections = append(selections, protocol.EnvironmentSelection{EnvironmentID: preview.EnvironmentID, Revision: preview.Revision})
		}
		for offset := 0; offset < len(selections); offset += 1000 {
			batch := selections[offset:min(offset+1000, len(selections))]
			data, err := json.Marshal(batch)
			if err != nil {
				continue
			}
			digest := sha256.Sum256(append(data, []byte(now.Truncate(time.Hour).Format(time.RFC3339))...))
			group.scope.Automatic = true
			_, err = d.startEnvironmentOperation(group.scope, protocol.EnvironmentOperationRequest{ID: hex.EncodeToString(digest[:]), Action: "clean_cache", Selections: batch})
			if err != nil {
				d.logger.Debug("capacity cache reclamation deferred", "error", err)
			}
		}
	}
}
