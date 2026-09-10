import { z } from "zod";

const digest = z.string().regex(/^[a-f0-9]{64}$/);
const oid = z.string().regex(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/);
export const localIndexCapabilitySchema = z.object({ local_review_index_supported: z.boolean().optional().catch(false) });
export function isLocalIndexMutation(action: string): boolean { return ["stage", "unstage", "commit"].includes(action); }
export function isLocalIndexAction(action: string): boolean { return action === "index" || isLocalIndexMutation(action); }

export const localIndexFileSchema = z.object({
  path: z.string().min(1).max(4096), old_path: z.string().max(4096).optional(),
  index_code: z.string().length(1), working_code: z.string().length(1),
  staged: z.boolean(), unstaged: z.boolean(), untracked: z.boolean(), conflicted: z.boolean(), unsupported: z.boolean(),
});
export const localIndexViewSchema = z.object({
  kind: z.literal("index"), version_id: digest, staged_version_id: digest.optional(), unstaged_version_id: digest.optional(), runtime_id: z.string().optional(),
  status: z.object({ index_id: digest, branch: z.string().min(1), head: oid, files: z.array(localIndexFileSchema).max(10000) }),
});
export const localIndexResultSchema = z.object({
  kind: z.literal("index_result"), runtime_id: z.string().optional(),
  result: z.object({ index_id: digest.optional(), commit: z.object({ commit: oid, parent: oid, tree: oid, branch: z.string().min(1), index_id: digest }).optional() }),
});
export type LocalIndexView = z.infer<typeof localIndexViewSchema>;
export type LocalIndexResult = z.infer<typeof localIndexResultSchema>;

export function parseLocalIndexResponse(request: { readonly action: string; readonly runtime_id?: string; readonly branch?: string; readonly head?: string; readonly index_id?: string }, raw: unknown): LocalIndexView | LocalIndexResult {
  const result = request.action === "index" ? localIndexViewSchema.parse(raw) : localIndexResultSchema.parse(raw);
  if (request.runtime_id && result.runtime_id && request.runtime_id !== result.runtime_id) throw new Error("Index runtime mismatch");
  if (result.kind === "index_result") {
    if (request.action === "commit") {
      const commit = result.result.commit;
      if (!commit || commit.parent !== request.head || commit.branch !== request.branch || commit.index_id !== request.index_id) throw new Error("Commit identity mismatch");
    } else if (!result.result.index_id || result.result.commit) throw new Error("Staging result missing");
  }
  return result;
}
