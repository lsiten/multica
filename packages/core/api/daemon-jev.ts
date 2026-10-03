import { z } from "zod";
import { parseWithFallback } from "./schema";

export const DaemonJevModelSchema = z.object({
  id: z.string(), revision: z.string(), engine_version: z.string().optional(),
  license: z.string().optional(), download_bytes: z.number().nonnegative().optional(),
  devices: z.array(z.string()).optional(),
}).loose();
export type DaemonJevModel = z.infer<typeof DaemonJevModelSchema>;
const DaemonJevModelsSchema = z.object({
  models: z.array(DaemonJevModelSchema).default([]),
  status: z.array(z.object({
    model_id: z.string(), revision: z.string().optional(), state: z.string(), phase: z.string().optional(), installed:z.boolean().optional(),
    downloaded_bytes: z.number().nonnegative().optional(), total_bytes: z.number().nonnegative().optional(),
    device: z.string().optional(), active_leases: z.number().nonnegative().optional(), error: z.string().optional(),
  }).loose()).default([]),
}).loose();
export type DaemonJevModels = z.infer<typeof DaemonJevModelsSchema>;

export function parseDaemonJevModels(value: unknown): DaemonJevModels {
  const parsed = parseWithFallback<DaemonJevModels | null>(value, DaemonJevModelsSchema, null, {endpoint:"daemon /jev/models"});
  if (!parsed) throw new Error("Invalid daemon Jev model response");
  return parsed;
}
export function parseDaemonJevModel(value: unknown): DaemonJevModel {
  const parsed = parseWithFallback<DaemonJevModel | null>(value, DaemonJevModelSchema, null, {endpoint:"daemon /jev/models/register"});
  if (!parsed) throw new Error("Invalid daemon Jev model registration response");
  return parsed;
}

const BuiltinMcpServicesSchema = z.object({
  services: z.array(z.object({name:z.string(),transport:z.string(),requires_capability:z.boolean().default(false),tools:z.array(z.object({name:z.string(),description:z.string().default(""),inputSchema:z.record(z.string(),z.unknown()).default({})}).loose()).default([])})),
  instances: z.array(z.object({name:z.string(),instance_id:z.string().optional(),workspace_id:z.string().optional(),enabled:z.boolean(),scope:z.string(),ready:z.boolean(),state:z.string(),reason:z.string().optional(),tool_count:z.number().optional(),checked_at:z.string().optional()})).default([]),
});
export type BuiltinMcpServices = z.infer<typeof BuiltinMcpServicesSchema>;
export function parseBuiltinMcpServices(value:unknown):BuiltinMcpServices{
 const parsed=parseWithFallback<BuiltinMcpServices|null>(value,BuiltinMcpServicesSchema,null,{endpoint:"daemon /mcp/services"});
 if(!parsed)throw new Error("Invalid built-in MCP service response");return parsed;
}
