import { z } from "zod";
import { parseWithFallback } from "../api/schema";

const managedWorktreeSchema = z.object({
  workspace_id: z.string(),
  task_short: z.string(),
  task_id: z.string().optional().default(""),
  runtime_id: z.string().optional(),
  repositories: z.array(z.string()).optional().default([]),
  path: z.string().min(1),
  agent_id: z.string().optional().default(""),
  agent_name: z.string().optional().default(""),
  kind: z.string(),
  size_bytes: z.number().finite().nonnegative(),
  active: z.boolean().optional().default(true),
  protection_reason: z.string().optional().default("unavailable"),
}).transform((row) => ({
  workspaceId: row.workspace_id,
  taskName: row.task_short,
  taskId: row.task_id,
  ...(row.runtime_id ? { runtimeId: row.runtime_id } : {}),
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
