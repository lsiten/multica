import { z } from "zod";

export const worktreeActions = ["review", "changes_requested", "merge", "cleanup", "retained", "unknown", "active"] as const;
const actionSchema = z.enum(worktreeActions).catch("unknown").default("unknown");
const timestamp = z.string().datetime({ offset: true }).optional().catch(undefined);

export const worktreeLifecycleSchema = z.object({
  run_status: z.string().optional().default(""),
  issue_id: z.string().nullish().transform((value) => value ?? ""),
  issue_status: z.string().optional().default(""),
  issue_status_category: z.string().optional().default(""),
  completed_at: timestamp,
  last_activity_at: timestamp,
  stale: z.boolean().optional().catch(false).default(false),
  next_action: actionSchema,
  repositories_details: z.array(z.object({
    path: z.string(), target: z.string().optional().default(""),
    review_state: z.string().optional().default(""), next_action: actionSchema,
    reason: z.string().optional().default(""),
  })).optional().default([]),
});

export function toWorktreeLifecycle(row: z.infer<typeof worktreeLifecycleSchema>) {
  return {
    runStatus: row.run_status, issueId: row.issue_id, issueStatus: row.issue_status,
    issueStatusCategory: row.issue_status_category, completedAt: row.completed_at,
    lastActivityAt: row.last_activity_at, stale: row.stale, nextAction: row.next_action,
    repositoryDetails: row.repositories_details.map((repository) => ({
      path: repository.path, target: repository.target, reviewState: repository.review_state,
      nextAction: repository.next_action, reason: repository.reason,
    })),
  };
}

export type WorktreeLifecycle = ReturnType<typeof toWorktreeLifecycle>;
export type WorktreeAction = typeof worktreeActions[number];

export function compareWorktreeLifecycle(a: WorktreeLifecycle, b: WorktreeLifecycle): number {
  return Number(b.stale) - Number(a.stale)
    || worktreeActions.indexOf(a.nextAction) - worktreeActions.indexOf(b.nextAction)
    || (a.lastActivityAt || a.completedAt || "9999").localeCompare(b.lastActivityAt || b.completedAt || "9999");
}
