import type { ManagedWorktree } from "./managed-worktree";
import type { WorktreeArchiveSummary } from "./worktree-archives";

export type WorktreeFilters = {
  workspace: string;
  agent: string;
  project: string;
  squad: string;
  environment: string;
  kind: string;
  activity: string;
  search: string;
};

export const emptyWorktreeFilters: WorktreeFilters = { workspace: "", agent: "", project: "", squad: "", environment: "", kind: "", activity: "", search: "" };

export function filterWorktreeInventory(rows: readonly ManagedWorktree[], filters: WorktreeFilters): ManagedWorktree[] {
  const search = filters.search.trim().toLocaleLowerCase();
  return rows.filter((row) => {
    if (filters.workspace && row.workspaceId !== filters.workspace) return false;
    if (filters.agent && row.agentId !== filters.agent) return false;
    if (filters.project && row.projectId !== filters.project) return false;
    if (filters.squad && row.squadId !== filters.squad) return false;
    if (filters.environment && row.environmentKind !== filters.environment) return false;
    if (filters.kind && row.kind !== filters.kind) return false;
    if (filters.activity === "active" && !row.active) return false;
    if (filters.activity === "inactive" && row.active) return false;
    if (filters.activity === "protected" && !row.protectionReason) return false;
    if (search && ![row.taskName, row.taskId, row.agentName, row.agentId, row.path, row.projectName, row.squadName].some((value) => value?.toLocaleLowerCase().includes(search))) return false;
    return true;
  });
}

export function filterWorktreeArchives(rows: readonly WorktreeArchiveSummary[], filters: WorktreeFilters): WorktreeArchiveSummary[] {
  const search=filters.search.trim().toLocaleLowerCase();
  return rows.filter((row) => {
    if (filters.workspace && row.workspaceId!==filters.workspace) return false;
    if (filters.agent && row.agentId!==filters.agent) return false;
    if (filters.project && row.projectId!==filters.project) return false;
    if (filters.squad && row.squadId!==filters.squad) return false;
    if (filters.kind && row.kind!==filters.kind) return false;
    return !search || [row.taskName,row.taskId,row.agentName,row.agentId,row.projectName,row.squadName,row.originalPath].some((value)=>value.toLocaleLowerCase().includes(search));
  });
}
