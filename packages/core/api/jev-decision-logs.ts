import { z } from "zod";

export const JevDecisionLogSummarySchema = z.object({
  id: z.string().min(1), workspace_id: z.string().min(1), task_id: z.string().min(1),
  agent_id: z.string().min(1), agent_name: z.string().default(""), issue_identifier: z.string().default(""),
  tool: z.string().default(""), model: z.string().default(""), error_code: z.string().default(""),
  source: z.enum(["agent_context", "local", "remote", "system_one", "unknown"]).catch("unknown"),
  result_class: z.enum(["running", "success", "error", "rejected", "unknown"]).catch("unknown"),
  started_at: z.string(), duration_ms: z.number().nonnegative().default(0),
});

export const JevDecisionLogListResponseSchema = z.object({
  items: z.array(JevDecisionLogSummarySchema), total: z.number().int().nonnegative(),
  limit: z.number().int().min(1).max(100), offset: z.number().int().nonnegative(), as_of: z.string(),
});

export const JevDecisionLogDetailSchema = JevDecisionLogSummarySchema.extend({
  model_revision: z.string().default(""), config_revision: z.number().int().nonnegative().default(0),
  device: z.string().default(""), completed_at: z.string().nullable().default(null),
  input: z.string().default(""), output: z.string().default(""),
  requests: z.array(z.object({
    attempt: z.number().int().positive(), variant: z.string().default(""), http_status: z.number().int().min(0).max(599),
    result_class: z.string().default(""), duration_ms: z.number().nonnegative().default(0),
    input: z.string().default(""), output: z.string().default(""), response_incomplete: z.boolean().default(false),
  })).default([]),
});
