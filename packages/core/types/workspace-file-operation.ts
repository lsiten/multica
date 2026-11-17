import { z } from "zod";
import { parseWithFallback } from "../api/schema";

export const workspaceFileOperationsCapabilitySchema = z.object({
  workspace_file_operations_supported: z.boolean().optional().catch(false),
  workspace_file_selection_supported: z.boolean().optional().catch(false),
});

const PROTECTED_PARTS = new Set([".git", ".multica", ".task_owner", ".task_lock", ".review-runtime.json", ".local-review-cache"]);
const operationPath = z.string().min(1).max(4096).refine((value) => !value.startsWith("/") && !/[\\\0:]/.test(value) && value.split("/").every((part) => !!part && part !== "." && part !== ".." && !/[. ]$/.test(part) && !PROTECTED_PARTS.has(part.toLowerCase())), "A relative working path is required");
const createFileSchema = z.object({ kind: z.literal("create_file"), path: operationPath, content: z.string().refine((value) => new TextEncoder().encode(value).length <= 256 * 1024).optional() }).strict();
const createDirectorySchema = z.object({ kind: z.literal("create_directory"), path: operationPath }).strict();
export const workspaceFileCreationSchema = z.discriminatedUnion("kind", [createFileSchema, createDirectorySchema]);
export type WorkspaceFileCreation = z.infer<typeof workspaceFileCreationSchema>;
export const workspaceFileOperationSchema = z.discriminatedUnion("kind", [
  createFileSchema,
  createDirectorySchema,
  z.object({ kind: z.literal("move"), path: operationPath, destination: operationPath, expected_file_id: z.string().uuid().optional() }).strict(),
  z.object({ kind: z.literal("trash"), path: operationPath, expected_file_id: z.string().uuid().optional() }).strict(),
  z.object({ kind: z.literal("restore"), path: operationPath, recovery_id: z.string().min(1).max(128) }).strict(),
]).refine((operation) => operation.kind !== "move" || (operation.destination !== operation.path && !operation.destination.startsWith(`${operation.path}/`)), "A directory cannot be moved inside itself");

export type WorkspaceFileOperation = z.infer<typeof workspaceFileOperationSchema>;

const resultSchema = z.object({
  kind: z.literal("file_operation"), command_id: z.string().min(1).max(128),
  operation_id: z.string().min(1).max(128).optional(),
  file_id: z.string().uuid().optional(), directory_id: z.string().regex(/^[0-9a-f]{64}$/).optional(), directory_revision: z.number().int().nonnegative().safe().optional(),
  operation: z.enum(["create_file", "create_directory", "move", "trash", "restore"]),
  path: operationPath, destination: operationPath.optional(), recovery_id: z.string().min(1).max(128).optional(), directory: z.boolean(),
});
export type WorkspaceFileOperationResult = z.infer<typeof resultSchema>;

const trashPageSchema = z.object({
  kind: z.literal("workspace_trash"), path: z.string(),
  items: z.array(z.object({ path: operationPath, recovery_id: z.string().min(1).max(128), directory: z.boolean(), deleted_at: z.string().datetime().optional(), actor_id: z.string().optional().default("") })).max(100),
  next_offset: z.number().int().nonnegative().safe(), has_more: z.boolean(),
});
export type WorkspaceTrashPage = z.infer<typeof trashPageSchema>;

export function parseWorkspaceTrashPage(input: { readonly path: string; readonly offset: number; readonly limit: number }, raw: unknown): WorkspaceTrashPage {
  const page = parseWithFallback<WorkspaceTrashPage | null>(raw, trashPageSchema, null, { endpoint: "POST /api/local-reviews/execute" });
  if (!page || page.path !== input.path || page.next_offset - input.offset < page.items.length || page.next_offset < input.offset || page.next_offset > input.offset + input.limit || page.items.length > input.limit || (page.has_more && page.next_offset === input.offset)) throw new Error("Invalid workspace trash page");
  return page;
}

export function parseWorkspaceFileOperationResult(input: { readonly command_id?: string; readonly file_operation?: WorkspaceFileOperation }, raw: unknown): WorkspaceFileOperationResult {
  const result = parseWithFallback<WorkspaceFileOperationResult | null>(raw, resultSchema, null, { endpoint: "POST /api/local-reviews/execute" });
  const operation = input.file_operation;
  if (!result || !operation || result.command_id !== input.command_id || result.operation !== operation.kind || result.path !== operation.path) throw new Error("File operation receipt does not match the request");
  let destination: string | undefined;
  let recovery: string | undefined;
  switch (operation.kind) {
    case "move": destination = operation.destination; break;
    case "trash": recovery = input.command_id; break;
    case "restore": recovery = operation.recovery_id; break;
    case "create_file":
      if (result.directory) throw new Error("File operation receipt identifies a directory");
      break;
    case "create_directory":
      if (!result.directory) throw new Error("File operation receipt identifies a file");
      break;
    default: { const unhandled: never = operation; return unhandled; }
  }
  if (result.destination !== destination || result.recovery_id !== recovery) throw new Error("File operation receipt identifies another file");
  return result;
}
