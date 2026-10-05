import { z } from "zod";
export const ProgressActionResultSchema = z.object({ task_id: z.string().uuid().nullable(), status: z.string().min(1), issue_id: z.string().uuid() });

const count = z.number().int().nonnegative();
const ProgressIssueRefSchema = z.object({
  id: z.string().min(1), identifier: z.string(), title: z.string(), status: z.string().min(1),
  status_category: z.string().default("unknown"), status_name: z.string().default(""),
  project_id: z.string().nullable().default(null), project_title: z.string().default(""),
});
const ProgressRequestSchema = z.object({
  id: z.string().min(1), recipient_id: z.string().min(1), needs_me: z.boolean().default(false), expires_at: z.string(),
});
const ProgressEntrySchema = z.object({
  issue: ProgressIssueRefSchema,
  priority: z.string().default("none"), assignee_type: z.string().default(""), assignee_id: z.string().nullable().default(null),
  path: z.array(ProgressIssueRefSchema).default([]), group: ProgressIssueRefSchema.nullable().default(null),
  reasons: z.array(z.string()).default([]), needs_me: z.boolean().default(false), attention: z.boolean().default(false),
  waiting_children: count.default(0), direct_blockers: z.array(ProgressIssueRefSchema).default([]), root_blockers: z.array(ProgressIssueRefSchema).default([]),
  blocked_issue_count: count.default(0), affected_goal_count: count.default(0),
  run: z.object({ id: z.string().min(1), status: z.string(), since: z.string(), summary: z.string().optional().default("") }).nullable().default(null),
  requests: z.array(ProgressRequestSchema).default([]), stage: z.number().int().positive().nullable().default(null),
  due_date: z.string().nullable().default(null), wait_since: z.string().default(""), rank: count.default(6),
  revision: count.optional(),
  next_step: z.object({ kind: z.string(), summary: z.string(), actor_type: z.string(), actor_id: z.string().optional(), missing: z.array(z.string()).optional(), request_id: z.string().optional(), evidence: z.array(z.string()).optional(), issue_revision: count, source_task_id: z.string().optional() }).nullable().optional().default(null).catch(null),
  actions: z.array(z.object({ kind: z.enum(["respond", "manual", "open_blocker", "assign", "resolve_runtime", "member_work", "review", "continue", "inspect_continue", "provide_info", "rerun", "unknown"]).catch("unknown"), actor_type: z.string(), actor_id: z.string().nullable(), needs_me: z.boolean().default(false), enabled: z.boolean().catch(false).default(false), disabled_reason: z.string().default(""), target_issue_id: z.string().optional(), request_id: z.string().optional() }).transform(action => action.kind === "unknown" ? { ...action, enabled: false, disabled_reason: "unsupported_action" } : action)).optional().default([]).catch([]),
});
export const IssueProgressViewSchema = z.object({
  workspace_id: z.string().min(1), scope: z.object({ type: z.enum(["issue", "project"]), id: z.string().min(1) }),
  as_of: z.string().min(1), version: z.string().min(1),
  summary: z.object({ total: count, done: count, closed: count, open: count, open_leaf: count, open_parent: count, attention: count, pending_decisions: count })
    .refine(summary => summary.done + summary.closed + summary.open === summary.total && summary.open_leaf + summary.open_parent === summary.open),
  root: ProgressEntrySchema.nullable().default(null), items: z.array(ProgressEntrySchema),
  project_requests: z.array(ProgressRequestSchema).default([]), complete: z.boolean().default(false), coverage_reasons: z.array(z.string()).default([]),
  next_cursor: z.string().nullable().default(null), has_more: z.boolean().default(false), filtered_total: count,
}).refine(view => !view.has_more || !!view.next_cursor);
