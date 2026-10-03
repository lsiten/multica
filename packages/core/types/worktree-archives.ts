import { z } from "zod";
import { parseWithFallback } from "../api/schema";

const archiveIdentitySchema = z.string().regex(/^[a-f0-9]{64}$/);
const archiveSelectionSchema = z.object({ environmentId: archiveIdentitySchema, revision: archiveIdentitySchema });

export const worktreeArchiveRequestSchema = z.discriminatedUnion("action", [
  z.object({ action: z.literal("preview"), workspaceId: z.string().min(1).optional() }),
  z.object({ action: z.literal("archive"), workspaceId: z.string().min(1).optional(), operationId: archiveIdentitySchema, selections: z.array(archiveSelectionSchema).min(1).max(1000) }),
  z.object({ action: z.literal("restore"), workspaceId: z.string().min(1).optional(), archiveId: archiveIdentitySchema }),
]);

const archiveResultSchema = z.object({
  environment_id: z.string(), workspace_id: z.string(), task_id: z.string(), revision: z.string(), archive_id: z.string(), reason: z.string(),
  original_bytes: z.number().finite().nonnegative(), archive_bytes: z.number().finite().nonnegative(), reclaimed: z.boolean(), restored: z.boolean(),
}).transform((row) => ({
  environmentId: row.environment_id, workspaceId: row.workspace_id, taskId: row.task_id, revision: row.revision, archiveId: row.archive_id, reason: row.reason,
  originalBytes: row.original_bytes, archiveBytes: row.archive_bytes, reclaimed: row.reclaimed, restored: row.restored,
}));

const archiveSummarySchema = z.object({
  archive_id: archiveIdentitySchema, workspace_id: z.string().min(1), task_id: z.string().min(1), task_name: z.string(),
  agent_id: z.string(), agent_name: z.string(), runtime_id: z.string(), project_id: z.string(), project_name: z.string(), squad_id: z.string(), squad_name: z.string(),
  kind: z.string(), original_path: z.string().min(1), created_at: z.iso.datetime(), archive_bytes: z.number().finite().nonnegative(), logical_bytes: z.number().finite().nonnegative(), restore_reason: z.string(),
}).transform((row) => ({
  archiveId: row.archive_id, workspaceId: row.workspace_id, taskId: row.task_id, taskName: row.task_name, agentId: row.agent_id, agentName: row.agent_name,
  runtimeId: row.runtime_id, projectId: row.project_id, projectName: row.project_name, squadId: row.squad_id, squadName: row.squad_name,
  kind: row.kind, originalPath: row.original_path, createdAt: row.created_at, archiveBytes: row.archive_bytes, logicalBytes: row.logical_bytes, restoreReason: row.restore_reason,
}));

export type WorktreeArchiveRequest = z.infer<typeof worktreeArchiveRequestSchema>;
export type WorktreeArchiveSelection = z.infer<typeof archiveSelectionSchema>;
export type WorktreeArchiveResult = z.infer<typeof archiveResultSchema>;
export type WorktreeArchiveSummary = z.infer<typeof archiveSummarySchema>;

export function parseWorktreeArchiveResults(value: unknown): WorktreeArchiveResult[] {
  const result = parseWithFallback<WorktreeArchiveResult[] | null>(value, z.array(archiveResultSchema), null, { endpoint: "/worktrees/archives operation" });
  if (result === null) throw new Error("Invalid archive operation response; reload the inventory before retrying");
  return result;
}

export function parseWorktreeArchiveSummaries(value: unknown): WorktreeArchiveSummary[] {
  const result = parseWithFallback<WorktreeArchiveSummary[] | null>(value, z.array(archiveSummarySchema), null, { endpoint: "/worktrees/archives" });
  if (result === null) throw new Error("Invalid archive inventory response");
  return result;
}
