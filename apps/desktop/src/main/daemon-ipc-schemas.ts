import { z } from "zod";
import { parseWithFallback } from "@multica/core/api/schema";
import type {
  DaemonProcessOverviewResult,
} from "../shared/daemon-types";

const optionalString = z.string().optional();
const optionalNumber = z.number().finite().nonnegative().optional();
const capabilities = z.array(z.string()).optional();

const localApplicationSchema = z.object({
  application_id: z.string(),
  runtime_id: z.string(),
  workspace_id: z.string(),
  revision: z.union([z.string(), z.number()]),
  instance_id: optionalString,
  observation: z.unknown().optional(),
}).passthrough();

const workerProcessSchema = z.object({
  exec_id: z.string(),
  instance_id: z.string(),
  task_id: z.string(),
  provider: z.string(),
  state: z.string(),
  ready: z.boolean(),
  capabilities,
  started_at: z.string(),
  pid: z.number().int().positive().optional(),
  memory_bytes: optionalNumber,
  cpu_time_ms: optionalNumber,
}).passthrough();

const mcpInstanceSchema = z.object({
  name: z.string(),
  state: z.string(),
  ready: z.boolean(),
  enabled: z.boolean().optional(),
  reason: optionalString,
}).passthrough();

const mcpServiceSchema = z.object({
  name: z.string(),
  transport: z.string().optional(),
  requires_capability: z.boolean().optional(),
  tools: z.array(z.record(z.string(), z.unknown())).optional(),
}).passthrough();

const jevModelSchema = z.object({
  id: z.string(),
  revision: z.string(),
}).passthrough();

const jevStatusSchema = z.object({
  model_id: z.string(),
  state: z.string(),
  installed: z.boolean().optional(),
  downloaded_bytes: optionalNumber,
  total_bytes: optionalNumber,
}).passthrough();

const processOverviewEntrySchema = z.object({
  category: z.string(),
  id: z.string(),
  name: z.string(),
  state: z.string(),
  ready: z.boolean(),
  capabilities: z.array(z.string()).optional(),
  detail: z.string().optional(),
  started_at: z.string().optional(),
  pid: z.number().int().positive().optional(),
  memory_bytes: z.number().nonnegative().optional(),
  cpu_time_ms: z.number().nonnegative().optional(),
  stoppable: z.boolean().optional(),
  tree_memory_bytes: z.number().nonnegative().optional(),
  tree_cpu_time_ms: z.number().nonnegative().optional(),
  process_count: z.number().int().nonnegative().optional(),
}).loose();

const processOverviewSchema = z.object({
  processes: z.array(processOverviewEntrySchema).default([]),
  total: z.object({
    available: z.boolean().default(false),
    process_count: z.number().int().nonnegative().optional(),
    memory_bytes: z.number().nonnegative().optional(),
    cpu_time_ms: z.number().nonnegative().optional(),
  }).loose().default({ available: false }),
}).loose();

const listResponse = z.object({
  applications: z.array(localApplicationSchema).default([]),
}).loose();

const workerResponse = z.object({
  processes: z.array(workerProcessSchema).default([]),
}).loose();

const mcpReadinessResponse = z.object({
  mcp: z.array(mcpInstanceSchema).default([]),
}).loose();

const jevModelsResponse = z.object({
  models: z.array(jevModelSchema).default([]),
  status: z.array(jevStatusSchema).default([]),
}).loose();

const builtinMcpResponse = z.object({
  services: z.array(mcpServiceSchema).default([]),
  instances: z.array(mcpInstanceSchema).default([]),
}).loose();

const localApplicationLookupRequest = z.object({
  workspace_id: z.string().min(1),
  runtime_id: z.string().min(1),
  application_id: z.string().min(1),
  revision: z.number().int().nonnegative(),
});

const localApplicationOperationRequest = localApplicationLookupRequest.extend({
  action: z.enum(["start", "stop", "restart"]),
  command_id: z.string().min(1),
}).passthrough();

export function parseDaemonProcessOverview(raw: unknown): DaemonProcessOverviewResult {
  return parseWithFallback<DaemonProcessOverviewResult>(
    raw,
    processOverviewSchema,
    { processes: [], total: { available: false } },
    { endpoint: "daemon /runtimes/processes/overview", redactPayload: true },
  );
}

export function parseDaemonLocalApplications(raw: unknown): Record<string, unknown>[] {
  return parseWithFallback(raw, listResponse, { applications: [] }, {
    endpoint: "daemon /applications/local",
    redactPayload: true,
  }).applications;
}

export function parseDaemonWorkerProcesses(raw: unknown): Record<string, unknown>[] {
  return parseWithFallback(raw, workerResponse, { processes: [] }, {
    endpoint: "daemon /runtimes/processes",
    redactPayload: true,
  }).processes;
}

export function parseDaemonMcpReadiness(raw: unknown): Record<string, unknown>[] {
  return parseWithFallback(raw, mcpReadinessResponse, { mcp: [] }, {
    endpoint: "daemon /mcp/readiness",
    redactPayload: true,
  }).mcp;
}

export function parseDaemonJevModelsResponse(raw: unknown): Record<string, unknown> {
  return parseWithFallback(raw, jevModelsResponse, { models: [], status: [] }, {
    endpoint: "daemon /jev/models",
    redactPayload: true,
  });
}

export function parseDaemonBuiltinMcpResponse(raw: unknown): Record<string, unknown> {
  return parseWithFallback(raw, builtinMcpResponse, { services: [], instances: [] }, {
    endpoint: "daemon /mcp/services",
    redactPayload: true,
  });
}

export function parseLocalApplicationLookupRequest(raw: unknown): Record<string, unknown> | null {
  return parseWithFallback<Record<string, unknown> | null>(raw, localApplicationLookupRequest, null, {
    endpoint: "daemon local application lookup",
    redactPayload: true,
  });
}

export function parseLocalApplicationOperationRequest(raw: unknown): Record<string, unknown> | null {
  return parseWithFallback<Record<string, unknown> | null>(raw, localApplicationOperationRequest, null, {
    endpoint: "daemon local application operation",
    redactPayload: true,
  });
}
