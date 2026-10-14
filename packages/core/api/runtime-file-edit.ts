import { z } from "zod";
import { parseWithFallback } from "./schema";

// Runtime file editing — a human (member) or agent edit to a runtime working
// file. It is the write counterpart of local-review's read surface and shares
// the same "content version + optimistic lock" contract as
// document-content-version.ts. Two write modes:
//
//   "edit"       compare-and-swap write: the base digest must still match the
//                working file, otherwise the server answers 409 Conflict and the
//                caller reloads (or rebases) against the current content.
//   "merge_edit" three-way merge of (base -> new) onto the current working file;
//                a clean merge is written, an unresolvable conflict keeps the
//                runtime version (conflict: true) and the caller rebases.
//
// Both modes carry full content, not a textual diff: (base, new) is the change.
// git operations are never local-first (they stay on the daemon); binary files
// are read-only and never reach this endpoint.
//
// These schemas are the frontend contract: UI-consumed JSON is parsed through
// them with parseWithFallback (never an `as T` cast), so a newer backend can add
// fields without breaking installed clients.

const nonNegativeInt = z.number().int().nonnegative();

export const ApplyRuntimeFileEditRequestSchema = z
  .object({
    task_id: z.string().min(1),
    path: z.string().min(1),
    target: z.string().min(1),
    // Repository-relative path of the edited file (the daemon roots the write).
    relative_path: z.string().min(1).max(4096),
    // Stable id so a retried / redelivered edit is not applied twice.
    command_id: z.string().min(1).max(128),
    action: z.enum(["edit", "merge_edit"]),
    // Desktop local path scopes the write to a runtime (ownership check); the
    // web path omits these and the server resolves the runtime from the task.
    workspace_id: z.string().optional(),
    runtime_id: z.string().optional(),
    // Compare-and-swap ("edit"): digest of the content the editor opened.
    edit_base_sha: z.string().length(64).optional(),
    // Three-way merge ("merge_edit"): the content the editor opened (base).
    edit_base: z.string().optional(),
    // The edited content for both modes ("" truncates the file to empty).
    edit_new: z.string(),
  })
  .refine(
    (request) =>
      request.action === "edit" ? request.edit_base_sha !== undefined : request.edit_base !== undefined,
    { message: "edit requires edit_base_sha; merge_edit requires edit_base" },
  );

export type ApplyRuntimeFileEditRequest = z.infer<typeof ApplyRuntimeFileEditRequestSchema>;

// The applied edit / merge result. A merge reports merged/conflict; an unclean
// merge keeps the runtime version (conflict: true, new_content is the current
// content). An edit reports the new digest. new_content may be absent for a
// compare-and-swap write.
export const RuntimeFileEditResultSchema = z.object({
  file_path: z.string().min(1),
  // True when a three-way merge applied cleanly.
  merged: z.boolean().optional().default(false),
  // True when the merge could not be applied; the runtime version was kept and
  // the caller must rebase against the current content.
  conflict: z.boolean().optional().default(false),
  // Resulting content for a merge (fast path / clean merge / kept on conflict).
  new_content: z.string().optional().nullable(),
  // Digest of the content before the write.
  previous_sha256: z.string().optional().nullable(),
  // Digest after the write (edit) or of the merged content (merge).
  new_sha256: z.string().optional().nullable(),
  // Current digest after a conflict (the kept runtime version).
  current_sha256: z.string().optional().nullable(),
  bytes: nonNegativeInt.optional().default(0),
});

export type RuntimeFileEditResult = z.infer<typeof RuntimeFileEditResultSchema>;

// The relayed edit result is delivered under LocalReviewResult.edit as raw JSON;
// the desktop direct path returns the result unwrapped. Accept either shape.
export function parseRuntimeFileEditResult(raw: unknown): RuntimeFileEditResult | null {
  const candidate =
    raw && typeof raw === "object" && "edit" in raw ? (raw as { edit?: unknown }).edit : raw;
  return parseWithFallback<RuntimeFileEditResult | null>(
    candidate,
    RuntimeFileEditResultSchema,
    null,
    { endpoint: "POST /api/local-reviews/execute" },
  );
}
