import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import { toWorktreeLifecycle, worktreeLifecycleSchema } from "./worktree-lifecycle";

const managedWorktreeSchema = worktreeLifecycleSchema.extend({
  workspace_id: z.string(),
  task_short: z.string(),
  task_id: z.string().optional().default(""),
  runtime_id: z.string().optional(),
  environment_id: z.string().optional(),
  environment_kind: z.string().optional(),
  project_id: z.string().optional(),
  project_name: z.string().optional(),
  squad_id: z.string().optional(),
  squad_name: z.string().optional(),
  storage: z.object({
    code_bytes: z.number().finite().nonnegative(),
    output_bytes: z.number().finite().nonnegative(),
    log_bytes: z.number().finite().nonnegative(),
    runtime_bytes: z.number().finite().nonnegative(),
    other_bytes: z.number().finite().nonnegative(),
    allocated_bytes: z.number().finite().nonnegative().nullable(),
    shared_bytes: z.number().finite().nonnegative(),
  }).optional(),
  repositories: z.array(z.string()).optional().default([]),
  path: z.string().min(1),
  agent_id: z.string().optional().default(""),
  agent_name: z.string().optional().default(""),
  kind: z.string(),
  size_bytes: z.number().finite().nonnegative(),
  active: z.boolean().optional().default(true),
  protection_reason: z.string().optional().default("unavailable"),
}).transform((row) => ({
  ...toWorktreeLifecycle(row),
  workspaceId: row.workspace_id,
  taskName: row.task_short,
  taskId: row.task_id,
  ...(row.runtime_id ? { runtimeId: row.runtime_id } : {}),
  ...(row.environment_id ? { environmentId: row.environment_id } : {}),
  ...(row.environment_kind ? { environmentKind: row.environment_kind } : {}),
  ...(row.project_id ? { projectId: row.project_id, projectName: row.project_name } : {}),
  ...(row.squad_id ? { squadId: row.squad_id, squadName: row.squad_name } : {}),
  ...(row.storage ? { storage: {
    codeBytes: row.storage.code_bytes, outputBytes: row.storage.output_bytes,
    logBytes: row.storage.log_bytes, runtimeBytes: row.storage.runtime_bytes,
    otherBytes: row.storage.other_bytes, allocatedBytes: row.storage.allocated_bytes,
    sharedBytes: row.storage.shared_bytes,
  } } : {}),
  repositories: row.repositories,
  path: row.path,
  agentId: row.agent_id,
  agentName: row.agent_name,
  kind: row.kind,
  sizeBytes: row.size_bytes,
  active: row.active,
  protectionReason: row.protection_reason,
}));

const cleanupResultSchema = z.object({
  removed_paths: z.array(z.string()),
  retained: z.record(z.string(), z.string()),
}).transform((result) => ({ removedPaths: result.removed_paths, retained: result.retained }));

export type ManagedWorktree = z.infer<typeof managedWorktreeSchema>;
export type ManagedWorktreeCleanupResult = z.infer<typeof cleanupResultSchema>;

const cacheResultSchema = z.object({
  environment_id: z.string().min(1),
  workspace_id: z.string(),
  task_id: z.string(),
  revision: z.string(),
  candidates: z.array(z.object({ path: z.string().min(1), size_bytes: z.number().finite().nonnegative() })),
  size_bytes: z.number().finite().nonnegative(),
  removed_bytes: z.number().finite().nonnegative(),
  removed_count: z.number().int().nonnegative(),
  reason: z.string(),
}).transform((result) => ({
  environmentId: result.environment_id,
  workspaceId: result.workspace_id,
  taskId: result.task_id,
  revision: result.revision,
  candidates: result.candidates.map((candidate) => ({ path: candidate.path, sizeBytes: candidate.size_bytes })),
  sizeBytes: result.size_bytes,
  removedBytes: result.removed_bytes,
  removedCount: result.removed_count,
  reason: result.reason,
}));

export type WorktreeCacheResult = z.infer<typeof cacheResultSchema>;
export type WorktreeCacheSelection = { environmentId: string; revision: string };

const cacheSelectionSchema = z.object({ environmentId: z.string().regex(/^[a-f0-9]{64}$/), revision: z.string().regex(/^[a-f0-9]{64}$/) });

export const worktreeCacheRequestSchema = z.discriminatedUnion("action", [
  z.object({ action: z.literal("preview_cache"), workspace_id: z.string().min(1).optional() }),
  z.object({ action: z.literal("clean_cache"), workspace_id: z.string().min(1).optional(), selections: z.array(cacheSelectionSchema).min(1).max(1000) }),
]);

export function parseWorktreeCacheResults(value: unknown): WorktreeCacheResult[] {
  const result = parseWithFallback<WorktreeCacheResult[] | null>(value, z.array(cacheResultSchema), null, { endpoint: "/worktrees resources" });
  if (result === null) throw new Error("Invalid cache operation response; scan again before retrying");
  return result;
}

export function canCleanWorktree(row: ManagedWorktree): boolean {
  return !row.active && row.nextAction === "cleanup" && row.protectionReason === ""
    && ["completed", "failed", "cancelled"].includes(row.runStatus);
}

export function canDiscardWorktree(row: ManagedWorktree): boolean {
  return !row.active && row.nextAction !== "unknown" && ["completed", "failed", "cancelled"].includes(row.runStatus)
    && ["dirty", "unpushed", "output"].includes(row.protectionReason);
}

export function parseManagedWorktrees(value: unknown): ManagedWorktree[] {
  const result = parseWithFallback<ManagedWorktree[] | null>(value, z.array(managedWorktreeSchema), null, { endpoint: "/worktrees" });
  if (result === null) throw new Error("Invalid worktree inventory response");
  return result;
}

export function parseManagedWorktreeCleanup(value: unknown): ManagedWorktreeCleanupResult {
  const result = parseWithFallback<ManagedWorktreeCleanupResult | null>(value, cleanupResultSchema, null, { endpoint: "/worktrees cleanup" });
  if (result === null) throw new Error("Invalid worktree cleanup response; reload the inventory before retrying");
  return result;
}
