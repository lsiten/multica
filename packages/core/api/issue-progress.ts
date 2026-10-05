import { z } from "zod";

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
  run: z.object({ id: z.string().min(1), status: z.string(), since: z.string() }).nullable().default(null),
  requests: z.array(ProgressRequestSchema).default([]), stage: z.number().int().positive().nullable().default(null),
  due_date: z.string().nullable().default(null), wait_since: z.string().default(""), rank: count.default(6),
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
