import type { ManagedWorktree } from "./managed-worktree";

/** Counts physical checkouts once and separates execution records from code. */
export function summarizeWorktreeInventory(rows: readonly ManagedWorktree[]) {
  const physicalWorktrees = new Set<string>();
  const codeEnvironments = new Set<string>();
  const tasks = new Set<string>();
  let runRecords = 0;
  let pendingCleanup = 0;
  let unresolved = 0;
  for (const row of rows) {
    for (const id of row.physicalWorktreeIds) physicalWorktrees.add(id);
    if (row.codeEnvironmentId) codeEnvironments.add(row.codeEnvironmentId);
    if (row.environmentKind === "run_directory" && !row.active) runRecords++;
    if (row.retainedTaskId) tasks.add(row.issueId || row.retainedTaskId);
    if (row.retentionReason === "" && !row.active) pendingCleanup++;
    if (row.retentionReason === "unavailable" || row.protectionReason === "unowned" || row.protectionReason === "scope_changed") unresolved++;
  }
  return { physicalWorktrees: physicalWorktrees.size, codeEnvironments: codeEnvironments.size, tasks: tasks.size, runRecords, pendingCleanup, unresolved };
}
