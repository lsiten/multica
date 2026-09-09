import { z } from "zod";

export const localReviewCapabilitySchema = z.object({ local_review_supported: z.boolean().optional().catch(false) });
export const localReviewBranchesSchema = z.object({ branches: z.array(z.string()) });
export const localReviewRuntimeBindingSchema = z.object({ workspace_id: z.string().min(1), task_id: z.string().min(1), runtime_id: z.string().min(1) });
export const localReviewRuntimeHealthSchema = z.object({
  profile: z.string(),
  workspaces: z.array(z.object({ id: z.string(), runtimes: z.array(z.string()) })),
});

export const localReviewRequestSchema = z.object({
  task_id: z.string().min(1),
  workspace_id: z.string().min(1),
  runtime_id: z.string().optional(),
  path: z.string().min(1),
  target: z.string().min(1),
  action: z.enum(["read", "branches", "submit", "approve", "request_changes", "merge"]).optional(),
  snapshot_id: z.string().optional(),
  comment: z.string().max(8000).optional(),
  review_id: z.string().optional(),
  command_id: z.string().min(1).max(128).optional(),
});
export const localReviewBranchesRequestSchema = localReviewRequestSchema.extend({ target: z.string().optional().default("") });

export const localReviewSnapshotSchema = z.object({
  runtime_id: z.string().optional(),
  id: z.string().min(1),
  path: z.string(),
  branch: z.string(),
  target: z.string(),
  head: z.string(),
  target_head: z.string(),
  base: z.string(),
  dirty: z.boolean(),
  branches: z.array(z.string()),
  commits: z.string(),
  files: z.array(z.object({ path: z.string(), status: z.string(), patch: z.string() })),
  repositories: z.array(z.string()).nullish().transform((value) => value ?? []),
  review: z.object({ snapshot_id: z.string(), state: z.enum(["draft", "open", "approved", "changes_requested", "merged"]), comment: z.string(), merged_commit: z.string() }),
});

export type LocalReviewRequest = z.infer<typeof localReviewRequestSchema>;
export type LocalReviewSnapshot = z.infer<typeof localReviewSnapshotSchema>;

export const localReviewEventSchema = z.object({
  version_id: z.string().optional(),
  kind: z.string(), snapshot_id: z.string(), comment: z.string(),
  actor_id: z.string().optional().default(""), actor_name: z.string().nullish(), created_at: z.string(),
});

export const localReviewRelayResponseSchema = z.object({
  snapshot: localReviewSnapshotSchema.omit({ review: true }),
  review: localReviewSnapshotSchema.shape.review.extend({
    events: z.array(localReviewEventSchema).optional().default([]),
  }),
}).transform(({ snapshot, review }) => ({ ...snapshot, review, history: review.events }));

export const localReviewDirectResponseSchema = localReviewSnapshotSchema.extend({
  review: localReviewSnapshotSchema.shape.review.extend({ events: z.array(localReviewEventSchema).optional().default([]) }),
}).transform((snapshot) => ({ ...snapshot, history: snapshot.review.events }));

export const localMRSchema = z.object({
  id: z.string(), workspace_id: z.string(), runtime_id: z.string(), task_id: z.string(),
  issue_id: z.string().nullable(), agent_id: z.string(), repository_path: z.string(), target_branch: z.string(),
  snapshot_id: z.string(), snapshot: localReviewSnapshotSchema.omit({ review: true }).nullable(),
  state: z.enum(["draft", "open", "approved", "changes_requested", "merged"]),
  merged_commit: z.string(), command_status: z.string().optional(), command_error: z.string().optional(),
  events: z.array(z.object({ kind: z.string(), snapshot_id: z.string(), comment: z.string(), actor_id: z.string(), actor_name: z.string().nullish(), created_at: z.string() })).optional(),
});
export type LocalMR = z.infer<typeof localMRSchema>;

export const remoteWorktreesSchema = z.array(z.object({
  workspace_id: z.string(), task_id: z.string(), runtime_id: z.string(), agent_id: z.string(),
  work_dir: z.string(), status: z.string(), branch_name: z.string().nullable(),
}).transform((row) => ({
  workspaceId: row.workspace_id, taskId: row.task_id, runtimeId: row.runtime_id, agentId: row.agent_id,
  path: row.work_dir, taskName: row.branch_name || row.task_id, repositories: [row.work_dir],
  active: ["queued", "running", "dispatched", "waiting_local_directory"].includes(row.status),
})));
