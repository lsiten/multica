import { z } from "zod";
import { DocumentResourceSchema } from "../chat/document-reference";
import { parseWithFallback } from "./schema";

export const DocumentCommentResourceSchema = DocumentResourceSchema.pick({
  kind: true, id: true, taskId: true, projectId: true, projectResourceId: true, runtimeId: true, workDir: true, relativePath: true, fileId: true,
});
export const DocumentCommentAnchorSchema = z.object({
  quote: z.string().min(1).max(4000), start: z.number().int().nonnegative().safe(),
  prefix: z.string().max(200), suffix: z.string().max(200),
  revision: z.number().int().positive().optional(),
});
export const DocumentCommentSchema = z.object({
  id: z.string().uuid(), resource: DocumentCommentResourceSchema,
  parent_id: z.string().uuid().nullable().optional().default(null),
  anchor: DocumentCommentAnchorSchema.nullable().optional().default(null),
  content: z.string(), author_id: z.string().uuid(),
  resolved_at: z.string().nullable().optional().default(null),
  resolved_by: z.string().uuid().nullable().optional().default(null),
  revision: z.number().int().positive(), created_at: z.string(), updated_at: z.string(),
});
const DocumentCommentPageSchema = z.object({
  comments: z.array(DocumentCommentSchema).max(100),
  has_more: z.boolean().optional().default(false), next_offset: z.number().int().nonnegative(),
});
const CreateDocumentCommentSchema = z.object({
  submission_id: z.string().uuid().optional(),
  resource: DocumentCommentResourceSchema, content: z.string().trim().min(1).max(16000),
  parent_id: z.string().uuid().optional(), anchor: DocumentCommentAnchorSchema.optional(),
});
export type DocumentComment = z.infer<typeof DocumentCommentSchema>;
export type DocumentCommentAnchor = z.infer<typeof DocumentCommentAnchorSchema>;
export type DocumentCommentResource = z.infer<typeof DocumentCommentResourceSchema>;
export type CreateDocumentComment = z.infer<typeof CreateDocumentCommentSchema>;
type DocumentCommentPage = z.infer<typeof DocumentCommentPageSchema>;

type DocumentCommentsTransport = (path: string, options: {
  readonly method: "POST" | "PATCH";
  readonly headers: Readonly<Record<string, string>>;
  readonly body: string;
  readonly signal?: AbortSignal;
}) => Promise<unknown>;

export class DocumentCommentContractError extends Error {
  constructor(readonly operation: "read" | "create" | "resolve") {
    super(`Invalid document comment ${operation} receipt`);
    this.name = "DocumentCommentContractError";
  }
}

export function createDocumentCommentsApi(request: DocumentCommentsTransport) {
  return {
    async list(workspaceId: string, resource: DocumentCommentResource, offset: number, signal?: AbortSignal) {
      const endpoint = "/api/document-comments/query";
      const body = { resource: DocumentCommentResourceSchema.parse(resource), offset: z.number().int().nonnegative().parse(offset) };
      const raw = await request(endpoint, { method: "POST", headers: { "X-Workspace-ID": workspaceId, "X-Workspace-Slug": "" }, body: JSON.stringify(body), signal });
      const page = parseWithFallback<DocumentCommentPage | null>(raw, DocumentCommentPageSchema.nullable(), null, { endpoint, redactPayload: true });
      if (!page) throw new DocumentCommentContractError("read");
      return page;
    },
    async create(workspaceId: string, input: CreateDocumentComment) {
      const endpoint = "/api/document-comments";
      const body = CreateDocumentCommentSchema.parse(input);
      const raw = await request(endpoint, { method: "POST", headers: { "X-Workspace-ID": workspaceId, "X-Workspace-Slug": "" }, body: JSON.stringify(body) });
      const comment = parseWithFallback<DocumentComment | null>(raw, DocumentCommentSchema.nullable(), null, { endpoint, redactPayload: true });
      if (!comment || comment.parent_id !== (body.parent_id ?? null) || (body.submission_id && comment.id !== body.submission_id)) throw new DocumentCommentContractError("create");
      return comment;
    },
    async resolve(workspaceId: string, input: { readonly resource: DocumentCommentResource; readonly comment: DocumentComment; readonly resolved: boolean }) {
      const { comment, resolved } = input;
      const endpoint = `/api/document-comments/${encodeURIComponent(comment.id)}`;
      const raw = await request(endpoint, {
        method: "PATCH", headers: { "X-Workspace-ID": workspaceId, "X-Workspace-Slug": "" },
        body: JSON.stringify({ resource: DocumentCommentResourceSchema.parse(input.resource), resolved, expected_revision: comment.revision }),
      });
      const updated = parseWithFallback<DocumentComment | null>(raw, DocumentCommentSchema.nullable(), null, { endpoint, redactPayload: true });
      if (!updated || updated.id !== comment.id) throw new DocumentCommentContractError("resolve");
      return updated;
    },
  };
}
