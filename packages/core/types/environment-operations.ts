import { z } from "zod";
import { parseWithFallback } from "../api/schema";

const identity = z.string().regex(/^[a-f0-9]{64}$/);
const environmentRuntimeSchema = z.object({
  id: z.string().min(1), workspace_id: z.string().min(1), name: z.string(),
  daemon_id: z.string().nullable().optional().catch(null).default(null),
  provider: z.string().optional().catch("unknown").default("unknown"),
  custom_name: z.string().nullable().optional(), owner_id: z.string().nullable().optional().default(null),
  runtime_mode: z.enum(["local", "cloud", "unknown"]).catch("unknown"),
  status: z.enum(["online", "offline"]).catch("offline"),
  metadata: z.object({
    version: z.string().optional().catch(undefined),
    cli_version: z.string().optional().catch(undefined),
    launched_by: z.string().optional().catch(undefined),
    server_url: z.string().optional().catch(undefined),
  }).catch({}).default({}),
});
export type EnvironmentRuntime = z.infer<typeof environmentRuntimeSchema>;

export function parseEnvironmentRuntimes(value: unknown) {
  const rows = parseWithFallback<z.infer<typeof environmentRuntimeSchema>[] | null>(value, z.array(environmentRuntimeSchema), null, { endpoint: "environment runtimes" });
  if (rows === null) throw new Error("Invalid environment runtime list");
  return rows;
}
const selection = z.object({ environment_id: identity, revision: z.string().default("") });
export const environmentOperationRequestSchema = z.object({
  id: identity,
  action: z.enum(["clean_cache", "archive", "restore", "cleanup", "discard"]),
  selections: z.array(selection).max(1000).default([]),
  archive_id: identity.optional(),
}).superRefine((request, ctx) => {
  if (request.action === "restore") {
    if (!request.archive_id || request.selections.length > 0) ctx.addIssue({ code: "custom", message: "Restore requires only an archive identity" });
  } else {
    if (request.archive_id || request.selections.length === 0) ctx.addIssue({ code: "custom", message: "Environment selections required" });
    if (["clean_cache", "archive"].includes(request.action) && request.selections.some((entry) => !identity.safeParse(entry.revision).success)) ctx.addIssue({ code: "custom", message: "Preview revisions required" });
    if (new Set(request.selections.map((entry) => entry.environment_id)).size !== request.selections.length) ctx.addIssue({ code: "custom", message: "Unique environment identities required" });
  }
});

export const environmentPolicySchema = z.object({
  enabled: z.boolean(),
  archive_after_hours: z.number().int().min(0).max(87600),
  cache_after_hours: z.number().int().min(0).max(87600),
  pressure_cache_after_hours: z.number().int().min(0).max(87600),
  max_idle_environments: z.number().int().min(0).max(100000),
  max_directory_bytes: z.number().int().min(0).max(2 ** 50),
  minimum_free_bytes: z.number().int().min(0).max(2 ** 50),
}).refine((policy) => policy.cache_after_hours === 0 || policy.pressure_cache_after_hours <= policy.cache_after_hours, { message: "Pressure cache retention must not exceed normal cache retention" });
const environmentPolicyStatusSchema = z.object({
  workspace_id: z.string(), runtime_id: z.string(), policy: environmentPolicySchema,
  effective_enabled: z.boolean(), scan_interval_seconds: z.number().int().positive(),
  free_bytes: z.number().int().nonnegative().nullable(), last_scan_at: z.iso.datetime().nullable(),
  idle_environments: z.number().int().nonnegative(), directory_bytes: z.number().int().nonnegative(), under_pressure: z.boolean(),
});
export type EnvironmentPolicy = z.infer<typeof environmentPolicySchema>;
export type EnvironmentPolicyStatus = z.infer<typeof environmentPolicyStatusSchema>;
export function parseEnvironmentPolicyStatus(value: unknown): EnvironmentPolicyStatus {
  const result = parseWithFallback<EnvironmentPolicyStatus | null>(value, environmentPolicyStatusSchema, null, { endpoint: "environment policy" });
  if (!result) throw new Error("Invalid environment policy response");
  return result;
}

export const environmentCommandSchema = z.discriminatedUnion("action", [
  z.object({ action: z.enum(["inventory", "cache_preview", "archive_preview", "archives", "operations", "policy"]) }),
  z.object({ action: z.literal("policy_update"), policy: environmentPolicySchema }),
  z.object({ action: z.literal("operation_start"), operation: environmentOperationRequestSchema }),
  z.object({ action: z.enum(["operation_status", "operation_cancel"]), operation_id: identity }),
]);

const operationStatusSchema = z.object({
  id: identity, action: z.string(), status: z.enum(["running", "cancelling", "completed", "cancelled", "failed", "interrupted", "unknown"]).catch("unknown"),
  automatic: z.boolean().optional().default(false),
  workspace_id: z.string(), runtime_id: z.string(), daemon_id: z.string(), profile: z.string(),
  started_at: z.iso.datetime(), updated_at: z.iso.datetime(), completed: z.number().int().nonnegative(), total: z.number().int().nonnegative(),
  results: z.array(z.object({
    environment_id: z.string().optional().default(""), archive_id: z.string().optional().default(""), reason: z.string().default(""),
    reclaimed: z.boolean().optional().default(false), restored: z.boolean().optional().default(false),
    removed_bytes: z.number().finite().nonnegative().optional().default(0), removed_count: z.number().int().nonnegative().optional().default(0),
    original_bytes: z.number().finite().nonnegative().optional().default(0), archive_bytes: z.number().finite().nonnegative().optional().default(0),
  })), error: z.string(),
}).transform((row) => ({
  id: row.id, action: row.action, status: row.status, automatic: row.automatic, workspaceId: row.workspace_id, runtimeId: row.runtime_id, daemonId: row.daemon_id, profile: row.profile,
  startedAt: row.started_at, updatedAt: row.updated_at, completed: row.completed, total: row.total, error: row.error,
  results: row.results.map((result) => ({ environmentId: result.environment_id, archiveId: result.archive_id, reason: result.reason, reclaimed: result.reclaimed, restored: result.restored, removedBytes: result.removed_bytes, removedCount: result.removed_count, originalBytes: result.original_bytes, archiveBytes: result.archive_bytes })),
}));

export type EnvironmentCommand = z.infer<typeof environmentCommandSchema>;
export type EnvironmentOperationRequest = z.infer<typeof environmentOperationRequestSchema>;
export type EnvironmentOperationStatus = z.infer<typeof operationStatusSchema>;

export function parseEnvironmentOperationStatus(value: unknown): EnvironmentOperationStatus {
  const result = parseWithFallback<EnvironmentOperationStatus | null>(value, operationStatusSchema, null, { endpoint: "environment operation status" });
  if (result === null || result.completed > result.total) throw new Error("Invalid environment operation receipt");
  return result;
}

export function environmentOperationID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function environmentOperationBatches(action: "archive" | "clean_cache", selections: EnvironmentOperationRequest["selections"]): EnvironmentOperationRequest[] {
  if (new Set(selections.map((item) => item.environment_id)).size !== selections.length) throw new Error("Unique environment selections required");
  const requests: EnvironmentOperationRequest[] = [];
  for (let offset = 0; offset < selections.length; offset += 1000) {
    requests.push(environmentOperationRequestSchema.parse({ id: environmentOperationID(), action, selections: selections.slice(offset, offset + 1000) }));
  }
  return requests;
}

export function parseEnvironmentOperationList(value: unknown): EnvironmentOperationStatus[] {
  const result = parseWithFallback<EnvironmentOperationStatus[] | null>(value, z.array(operationStatusSchema), null, { endpoint: "environment operations" });
  if (result === null || result.some((item) => item.completed > item.total)) throw new Error("Invalid environment operation list");
  return result;
}
