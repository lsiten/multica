import type { WorktreeCacheResult } from "@multica/core/types/managed-worktree";
import { worktreeCacheRequestSchema } from "@multica/core/types/managed-worktree";

export async function requestWorktreeCaches(input: unknown, send: (request: unknown) => Promise<WorktreeCacheResult[]>): Promise<WorktreeCacheResult[]> {
  const request = worktreeCacheRequestSchema.parse(input);
  return send({
    action: request.action,
    workspace_id: request.workspace_id,
    ...(request.action === "clean_cache" ? { selections: request.selections.map((selection) => ({ environment_id: selection.environmentId, revision: selection.revision })) } : {}),
  });
}
