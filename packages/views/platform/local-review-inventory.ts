import { localReviewRuntimeHealthSchema, remoteWorktreesSchema } from "@multica/core/types/local-review";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";

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
    const repositories = inventory.filter((entry) => entry.workspaceId === row.workspaceId &&
      (!entry.runtimeId || entry.runtimeId === row.runtimeId))
      .flatMap((entry) => entry.repositories.filter((repository) =>
        entry.path === row.path || repository === row.path || repository.startsWith(`${row.path}/`)));
    return { ...row, repositories: [...new Set(repositories)] };
  });
}
