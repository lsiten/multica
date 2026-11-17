// @vitest-environment node
import { expect, it } from "vitest";
import { pagedReviewRequestSchema, parsePagedReviewResponse } from "./local-review-pages";

const request = { task_id: "task", workspace_id: "ws", path: "/root", action: "file_operation", command_id: "move-1", file_operation: { kind: "move", path: "from.md", destination: "to.md" } } as const;
const result = { kind: "file_operation", command_id: "move-1", operation: "move", path: "from.md", destination: "to.md", directory: false };

it("accepts a working-file operation without requiring a Git branch or version", () => {
  const parsed = pagedReviewRequestSchema.parse(request);
  expect(parsePagedReviewResponse(parsed, result)).toEqual(result);
});

it.each(["move", "trash"] as const)("preserves the selected file identity for %s and rejects malformed identities", (kind) => {
  const operation = { kind, path: "from.md", ...(kind === "move" ? { destination: "to.md" } : {}), expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" };
  expect(pagedReviewRequestSchema.parse({ ...request, file_operation: operation }).file_operation).toEqual(operation);
  expect(pagedReviewRequestSchema.safeParse({ ...request, file_operation: { ...operation, expected_file_id: "not-an-identity" } }).success).toBe(false);
});

it.each(["../outside", "/absolute", ".git/config", ".multica/config", "folder/../other", "folder\\other", ".task_owner"])("rejects a protected or unscoped source %s", (path) => {
  expect(pagedReviewRequestSchema.safeParse({ ...request, file_operation: { ...request.file_operation, path } }).success).toBe(false);
});

it("does not grant write operations to a bound project directory", () => {
  expect(pagedReviewRequestSchema.safeParse({ ...request, task_id: undefined, project_resource_id: "project-directory" }).success).toBe(false);
});

it.each([
  { command_id: "another-command" },
  { path: "another-file.md" },
  { destination: "another-destination.md" },
  { operation: "trash", recovery_id: "move-1" },
])("rejects a receipt for a different operation: %j", (change) => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...result, ...change })).toThrow(/receipt/);
});

it.each(["create_file", "create_directory"] as const)("rejects a receipt with the wrong entry type for %s", (kind) => {
  const input = pagedReviewRequestSchema.parse({ ...request, file_operation: { kind, path: "new-entry" } });
  expect(() => parsePagedReviewResponse(input, { kind: "file_operation", command_id: "move-1", operation: kind, path: "new-entry", directory: kind === "create_file" })).toThrow(/receipt/);
});

it("accepts a partial trash page with no matches while more journal entries remain", () => {
  const input = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/root", action: "workspace_trash", offset: 10, limit: 2 });
  expect(parsePagedReviewResponse(input, { kind: "workspace_trash", path: "/root", items: [], next_offset: 12, has_more: true })).toMatchObject({ next_offset: 12, has_more: true });
  expect(() => parsePagedReviewResponse(input, { kind: "workspace_trash", path: "/root", items: [], next_offset: 10, has_more: true })).toThrow(/trash page/);
  expect(() => parsePagedReviewResponse(input, { kind: "workspace_trash", path: "/another-root", items: [], next_offset: 12, has_more: true })).toThrow(/trash page/);
});

it("rejects a malformed receipt before any caller can treat it as a completed operation", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...result, directory: "yes" })).toThrow(/receipt/);
});

it("retains the server admission identity and rejects malformed admission metadata", () => {
  const parsed = pagedReviewRequestSchema.parse(request);
  expect(parsePagedReviewResponse(parsed, { ...result, operation_id: "server-admission" })).toMatchObject({ operation_id: "server-admission" });
  expect(() => parsePagedReviewResponse(parsed, { ...result, operation_id: 42 })).toThrow(/receipt/);
});

it("retains the authoritative resource and directory revision on a completed operation", () => {
  const parsed = pagedReviewRequestSchema.parse(request);
  const metadata = { file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c", directory_id: "a".repeat(64), directory_revision: 4 };
  expect(parsePagedReviewResponse(parsed, { ...result, ...metadata })).toMatchObject(metadata);
  expect(() => parsePagedReviewResponse(parsed, { ...result, ...metadata, file_id: "malformed" })).toThrow(/receipt/);
  expect(() => parsePagedReviewResponse(parsed, { ...result, ...metadata, directory_revision: -1 })).toThrow(/receipt/);
});
