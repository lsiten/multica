import { z } from "zod";

// Document content versions — the write-side log for "based-on-document"
// conversation + editing. A version is exactly one of a text body (content) or
// a binary blob reference (content_url). See server handler
// DocumentContentVersionResponse / RecordDocumentContentVersionRequest.
//
// These schemas are the frontend contract: UI-consumed JSON is parsed through
// them with parseWithFallback (never an `as T` cast), so a newer backend can
// add fields without breaking installed clients.

const nonNegativeInt = z.number().int().nonnegative();

export const DocumentContentVersionSchema = z.object({
  id: z.string().uuid(),
  // "issue" = issue.description body (Markdown/HTML text); "attachment" = a
  // binary document (image / PDF) that is versioned, never edited in place.
  owner_type: z.enum(["issue", "attachment"]),
  owner_id: z.string().uuid(),
  revision: z.number().int().positive(),
  content_type: z.string(),
  // Text body. Absent for a binary version.
  content: z.string().nullable().optional().default(null),
  // Binary blob reference. Absent for a text version.
  content_url: z.string().nullable().optional().default(null),
  size_bytes: nonNegativeInt.default(0),
  // "member" = a human edit; "agent" = an agent edit (merge-into-one-version
  // per run, D4).
  actor_type: z.enum(["member", "agent"]),
  actor_id: z.string().uuid(),
  // True when this row is the D4 merge of consecutive same-actor edits.
  merged: z.boolean().default(false),
  note: z.string().nullable().optional().default(null),
  created_at: z.string(),
});

export const DocumentContentVersionListSchema = z.array(DocumentContentVersionSchema);

// The commit body. Exactly one of content / content_url is expected (the
// backend enforces it); the client may send either or both and let the server
// validate.
export const RecordDocumentContentVersionRequestSchema = z.object({
  content_type: z.string().min(1),
  content: z.string().nullable().optional(),
  content_url: z.string().nullable().optional(),
  size_bytes: nonNegativeInt.optional(),
  note: z.string().nullable().optional(),
  // Optimistic lock: the revision the writer believes is current. A mismatch
  // yields 409 Conflict; the client then retries off the fresh revision.
  expected_revision: z.number().int().positive().optional(),
  // Fold consecutive same-actor edits into the actor's latest version (D4).
  merge: z.boolean().optional(),
});

export type DocumentContentVersion = z.infer<typeof DocumentContentVersionSchema>;
export type DocumentContentVersionList = z.infer<typeof DocumentContentVersionListSchema>;
export type RecordDocumentContentVersionRequest = z.infer<
  typeof RecordDocumentContentVersionRequestSchema
>;
