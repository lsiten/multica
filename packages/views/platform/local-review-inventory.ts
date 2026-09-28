import { localReviewRuntimeHealthSchema, remoteWorktreesSchema } from "@multica/core/types/local-review";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";
import type { QueryClient } from "@tanstack/react-query";

export function invalidateWorktreeInventory(client: QueryClient) {
  return client.invalidateQueries({ predicate: ({ queryKey }) =>
    ["desktop-worktrees", "local-review-worktrees", "local-review-task-worktrees", "review-entry-repositories"].some((key) => queryKey[0] === key),
  });
}

export async function reconcileLocalReviewInventory(rows: ReturnType<typeof remoteWorktreesSchema.parse>) {
  if (typeof window === "undefined") return rows;
  const daemon: unknown = Reflect.get(window, "daemonAPI");
  if (!daemon || typeof daemon !== "object" || !("reviewInventory" in daemon) || typeof daemon.reviewInventory !== "function") return rows;
  const response: unknown = await daemon.reviewInventory();
  if (response === null) return rows;
  if (typeof response !== "object" || response === null || !("health" in response) || !("worktrees" in response)) throw new Error("Invalid local worktree inventory response");
  const health = localReviewRuntimeHealthSchema.parse(response.health);
  const inventory = parseManagedWorktrees(response.worktrees);
  return rows.map((row) => {
    const owned = health.workspaces.some((workspace) => workspace.id === row.workspaceId && workspace.runtimes.includes(row.runtimeId));
    if (!owned) return row;
    const local = inventory.filter((entry) => entry.workspaceId === row.workspaceId &&
      (!entry.runtimeId || entry.runtimeId === row.runtimeId) &&
      (entry.path === row.path || entry.repositories.some((repository) => repository === row.path || repository.startsWith(`${row.path}/`))));
    const repositories = local
      .flatMap((entry) => entry.repositories.filter((repository) =>
        entry.path === row.path || repository === row.path || repository.startsWith(`${row.path}/`)));
    const entry = local.find((entry) => entry.taskId === row.taskId);
    return { ...row, ...(entry?.runStatus ? {
      runStatus: entry.runStatus, issueId: entry.issueId, issueStatus: entry.issueStatus,
      issueStatusCategory: entry.issueStatusCategory, completedAt: entry.completedAt,
      lastActivityAt: entry.lastActivityAt, stale: entry.stale, nextAction: entry.nextAction,
      repositoryDetails: entry.repositoryDetails,
    } : entry ? { nextAction: "unknown" as const, repositoryDetails: [] } : {}), repositories: [...new Set(repositories)] };
  });
}
