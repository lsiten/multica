import { z } from "zod";

export const localApplicationRequestSchema = z.object({
  workspace_id:z.string().uuid(),runtime_id:z.string().uuid(),application_id:z.string().uuid(),
  action:z.enum(["start","stop","restart"]),revision:z.number().int().positive(),command_id:z.string().uuid(),
}).strict();

export const localApplicationInventorySchema = z.object({applications:z.array(z.object({
  workspace_id:z.string().uuid(),runtime_id:z.string().uuid(),application_id:z.string().uuid(),revision:z.number().int().positive(),
}).loose())});

export const localApplicationResultSchema = z.object({command_id:z.string().uuid(),sync_pending:z.boolean(),observation:z.object({instance_id:z.string().uuid(),generation:z.number().int().positive(),revision:z.number().int().positive(),process_state:z.string()}).loose()});
