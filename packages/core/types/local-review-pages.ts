import { z } from "zod";
import { localReviewEventSchema } from "./local-review";
import { selectedMergeResultSchema, type SelectedMergeResult } from "./local-review-selection";
import { isLocalIndexAction, isLocalIndexMutation, parseLocalIndexResponse, type LocalIndexView, type LocalIndexResult } from "./local-review-index";

const digest = z.string().regex(/^[0-9a-f]{64}$/);
const count = z.number().int().nonnegative().safe();
const blob = z.object({ id: digest, size: count });
const preview = z.enum(["text", "binary", "too_large", "uncached", "unsupported"]).catch("unsupported");
export const pagedReviewCapabilitySchema = z.object({ local_review_paging_supported: z.boolean().optional().catch(false) });

export function isPagedReviewDecision(action: string): boolean {
  return ["submit", "approve", "request_changes", "merge", "merge_selected"].includes(action) || isLocalIndexMutation(action);
}

export const pagedReviewRequestSchema = z.object({
  task_id: z.string().min(1), workspace_id: z.string().min(1), runtime_id: z.string().optional(),
  path: z.string().min(1).max(4096), target: z.string().max(250).default(""),
  action: z.enum(["merge_selected", "index", "stage", "unstage", "commit", "repositories", "manifest", "files", "file", "context", "content", "commits", "lease", "submit", "approve", "request_changes", "merge"]).default("manifest"),
  index_id: digest.optional(), head: z.string().regex(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/).optional(), branch: z.string().max(250).optional(),
  paths: z.array(z.string().min(1).max(4096)).max(10000).optional(), message: z.string().max(8000).optional(),
  version_id: digest.optional(), file_path: z.string().min(1).max(4096).optional(),
  side: z.enum(["old", "new"]).default("new"),
  offset: count.default(0), limit: count.min(1).max(65536).default(100),
  snapshot_id: digest.optional(), command_id: z.string().min(1).max(128).optional(),
  comment: z.string().max(8000).optional(),
}).superRefine((request, context) => {
  if (request.action !== "index" && request.action !== "repositories" && !request.target.trim()) context.addIssue({ code: "custom", message: "Target branch required" });
  if (request.action !== "index" && request.action !== "manifest" && request.action !== "repositories" && !request.version_id) context.addIssue({ code: "custom", message: "Review version required" });
  if ((request.action === "file" || request.action === "context" || request.action === "content") && !request.file_path) context.addIssue({ code: "custom", message: "Review file required" });
  if (request.action !== "content" && request.limit > 500) context.addIssue({ code: "custom", message: "Review page limit exceeded" });
  if (isPagedReviewDecision(request.action) && (!request.command_id || request.snapshot_id !== request.version_id)) context.addIssue({ code: "custom", message: "Matching reviewed version and operation identity required" });
  if (isLocalIndexMutation(request.action)) {
    if ((request.paths?.length ?? 0) > 1000) context.addIssue({ code: "custom", message: "Index selection limit exceeded" });
    if (!request.index_id || !request.head || !request.branch) context.addIssue({ code: "custom", message: "Reviewed index identity required" });
    if (request.action === "commit" ? !request.message?.trim() : !request.paths?.length) context.addIssue({ code: "custom", message: "Selected files or commit message required" });
  }
  if (request.action === "merge_selected" && (!request.paths?.length || !request.message?.trim() || new Set(request.paths).size !== request.paths.length)) context.addIssue({ code: "custom", message: "Unique selected files and message required" });
});

export const reviewVersionFileSchema = z.object({
  path: z.string(), old_path: z.string().optional(), status: z.string(),
  old_mode: z.string().optional(), new_mode: z.string().optional(),
  old: blob.optional(), new: blob.optional(), old_oid: z.string().optional(), new_oid: z.string().optional(),
  old_cached: z.boolean().optional(), new_cached: z.boolean().optional(),
  preview, additions: count, deletions: count,
});
export const reviewVersionHeaderSchema = z.object({
  repository: z.string(), branch: z.string(), target: z.string(),
  head: z.string(), target_head: z.string(), base: z.string(), dirty: z.boolean(), committed: z.boolean(),
});
export const reviewManifestSchema = z.object({
  version_id: digest, runtime_id: z.string().optional(), header: reviewVersionHeaderSchema,
  page: z.object({
    files: z.array(reviewVersionFileSchema).max(200), total_files: count, next_offset: count, has_more: z.boolean(),
    additions: count, deletions: count,
  }),
  review: z.object({
    snapshot_id: digest, state: z.enum(["draft", "open", "approved", "changes_requested", "merged"]).catch("draft"),
    comment: z.string(), merged_commit: z.string(), events: z.array(localReviewEventSchema).optional().default([]),
  }),
});
export const reviewFilePageSchema = z.object({
  version_id: digest, runtime_id: z.string().optional(), path: z.string(), preview, reason: z.string().optional(),
  page: z.object({
    lines: z.array(z.object({
      text: z.string().max(65536), kind: z.enum(["meta", "context", "add", "remove"]).catch("meta"),
      old_line: count.optional(), new_line: count.optional(),
      context_old_start: count.optional(), context_new_start: count.optional(), context_lines: count.optional(),
    })).max(500),
    next_line: count, has_more: z.boolean(),
  }).optional(),
});

export type PagedReviewInput = z.input<typeof pagedReviewRequestSchema>;
export const reviewRepositoriesSchema = z.object({ repositories: z.array(z.string()).max(1000), runtime_id: z.string().optional() });
export const reviewCommitsSchema = z.object({ version_id: digest, runtime_id: z.string().optional(), commits: z.array(z.object({ sha: z.string(), subject: z.string() })).max(100), next_offset: count, has_more: z.boolean() });
export const reviewContentSchema = z.object({
  version_id: digest, runtime_id: z.string().optional(), path: z.string(), side: z.enum(["old", "new"]),
  content: z.object({ text: z.string().max(262144), encoding: z.enum(["utf8", "hex"]), offset: count, next_offset: count, size: count, has_more: z.boolean() }),
});
export const reviewLeaseSchema = z.object({ version_id: digest, runtime_id: z.string().optional(), expires_at: z.string().datetime() });
export type PagedReviewRequest = z.infer<typeof pagedReviewRequestSchema>;
export type ReviewManifest = z.infer<typeof reviewManifestSchema>;
export type ReviewFilePage = z.infer<typeof reviewFilePageSchema>;
export type PagedReviewResponse = SelectedMergeResult | LocalIndexView | LocalIndexResult | ReviewManifest | ReviewFilePage | z.infer<typeof reviewRepositoriesSchema> | z.infer<typeof reviewCommitsSchema> | z.infer<typeof reviewContentSchema> | z.infer<typeof reviewLeaseSchema>;

export function parsePagedReviewResponse(request: PagedReviewRequest, raw: unknown): PagedReviewResponse {
  if (request.action === "merge_selected") {
    const result = selectedMergeResultSchema.parse(raw);
    if (result.version_id !== request.version_id || result.target !== request.target || JSON.stringify([...result.paths].sort()) !== JSON.stringify([...(request.paths ?? [])].sort()) || (request.runtime_id && result.runtime_id && request.runtime_id !== result.runtime_id)) throw new Error("Selected merge identity mismatch");
    return result;
  }
  if (isLocalIndexAction(request.action)) return parseLocalIndexResponse(request, raw);
  if (request.action === "lease") {
    const result = reviewLeaseSchema.parse(raw);
    if (result.version_id !== request.version_id || (result.runtime_id && request.runtime_id && result.runtime_id !== request.runtime_id)) throw new Error("Review lease identity mismatch");
    return result;
  }
  if (request.action === "content") {
    const result = reviewContentSchema.parse(raw);
    const content = result.content;
    const bytes = content.next_offset - content.offset;
    if (result.version_id !== request.version_id || result.path !== request.file_path || result.side !== request.side) throw new Error("Review content identity mismatch");
    if (content.offset !== request.offset || bytes < 0 || bytes > request.limit + 3 || content.next_offset > content.size || content.has_more !== (content.next_offset < content.size) || (content.has_more && bytes === 0)) throw new Error("Invalid review content pagination");
    if (content.encoding === "utf8" && new TextEncoder().encode(content.text).length !== bytes) throw new Error("Review content byte count mismatch");
    if (content.encoding === "hex" && (!/^[0-9a-f\s]*$/.test(content.text) || content.text.replace(/\s/g, "").length !== bytes * 2)) throw new Error("Invalid hexadecimal review content");
    return result;
  }
  if (request.action === "repositories") return reviewRepositoriesSchema.parse(raw);
  if (request.action === "commits") {
    const result = reviewCommitsSchema.parse(raw);
    if (result.version_id !== request.version_id || result.next_offset !== request.offset + result.commits.length || (result.has_more && result.commits.length === 0)) throw new Error("Invalid review commit pagination");
    return result;
  }
  const response = request.action === "file" || request.action === "context" ? reviewFilePageSchema.parse(raw) : reviewManifestSchema.parse(raw);
  if (request.version_id && response.version_id !== request.version_id) throw new Error("Review version mismatch");
  if (request.runtime_id && response.runtime_id && request.runtime_id !== response.runtime_id) throw new Error("Review runtime mismatch");
  if ("header" in response) {
    const page = response.page;
    const offset = isPagedReviewDecision(request.action) ? 0 : request.offset;
    if (response.header.target !== request.target || response.review.snapshot_id !== response.version_id) throw new Error("Review identity mismatch");
    if (page.next_offset !== offset + page.files.length || page.next_offset > page.total_files || page.has_more !== (page.next_offset < page.total_files)) throw new Error("Invalid review file pagination");
  } else {
    if (response.path !== request.file_path) throw new Error("Review file mismatch");
    if (response.preview === "text" && !response.page) throw new Error("Review patch page missing");
    if (response.page && (response.page.next_line !== request.offset + response.page.lines.length || (response.page.has_more && response.page.next_line <= request.offset))) throw new Error("Invalid review patch pagination");
  }
  return response;
}
