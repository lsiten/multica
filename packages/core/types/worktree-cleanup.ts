import { z } from "zod";
import { parseWithFallback } from "../api/schema";

const cleanupPreviewSchema = z.object({
  environment_id: z.string(), workspace_id: z.string(), task_id: z.string(), revision: z.string(), reason: z.string(),
  original_bytes: z.number().finite().nonnegative(), removed_bytes: z.number().finite().nonnegative(), reclaimed: z.boolean(),
}).transform((row) => ({ environmentId: row.environment_id, workspaceId: row.workspace_id, taskId: row.task_id, revision: row.revision, reason: row.reason, originalBytes: row.original_bytes, removedBytes: row.removed_bytes, reclaimed: row.reclaimed }));

export type WorktreeCleanupPreview = z.infer<typeof cleanupPreviewSchema>;

export function parseWorktreeCleanupPreviews(value: unknown): WorktreeCleanupPreview[] {
  const rows = parseWithFallback<WorktreeCleanupPreview[] | null>(value, z.array(cleanupPreviewSchema), null, { endpoint: "worktree cleanup preview" });
  if (rows === null) throw new Error("Invalid worktree cleanup preview");
  return rows;
}
