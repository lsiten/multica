// @vitest-environment node
import { expect, it } from "vitest";
import { pagedReviewRequestSchema, parsePagedReviewResponse } from "./local-review-pages";

const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt", offset: 0, limit: 20 } as const;
const response = { version_id: id, path: "file.txt", preview: "text", page: { lines: [{ text: "+change", kind: "add", new_line: 1 }], next_line: 1, has_more: true } };

it("accepts one project directory scope and rejects ambiguous scopes or writes", () => {
  const directory = { workspace_id: "ws", project_resource_id: "resource", path: "/project", action: "workspace_files" } as const;
  expect(pagedReviewRequestSchema.parse(directory)).toMatchObject({ task_id: "", project_resource_id: "resource" });
  expect(pagedReviewRequestSchema.safeParse({ ...directory, task_id: "task" }).success).toBe(false);
  expect(pagedReviewRequestSchema.safeParse({ ...request, project_resource_id: "resource", task_id: "", action: "stage" }).success).toBe(false);
});

it("validates explicit review scope against the resource identity", () => {
  expect(pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "index", scope: "run" })).toMatchObject({ scope: "run" });
  expect(pagedReviewRequestSchema.parse({ project_resource_id: "resource", workspace_id: "ws", path: "/repo", action: "index", scope: "directory" })).toMatchObject({ scope: "directory" });
  expect(pagedReviewRequestSchema.parse({ ...request, scope: "version" })).toMatchObject({ scope: "version" });
  expect(pagedReviewRequestSchema.safeParse({ project_resource_id: "resource", workspace_id: "ws", path: "/repo", action: "index", scope: "run" }).success).toBe(false);
  expect(pagedReviewRequestSchema.safeParse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "index", scope: "directory" }).success).toBe(false);
  expect(pagedReviewRequestSchema.safeParse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "manifest", target: "main", scope: "version" }).success).toBe(false);
});

it("parses bounded patch pages", () => {
  expect(parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), response)).toMatchObject(response);
});

it("parses a version directory without requiring a target branch", () => {
  const input = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "versions" });
  const page = { versions: [{ version_id: id, updated_at: "2026-11-16T10:00:00.000Z", header: { repository: "/repo", branch: "feature", target: "main", head: "b".repeat(40), target_head: "c".repeat(40), base: "d".repeat(40), dirty: true, committed: false } }] };
  expect(parsePagedReviewResponse(input, page)).toEqual(page);
});

it("retains server file identities on directory and content pages and rejects malformed identities", () => {
  const fileId = "26d9d04d-5c45-40b6-bb58-c60e7d59433c";
  const directory = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "workspace_files" });
  const page = { path: "/repo", directory_id: id, entries: [{ path: "notes.md", file_id: fileId, size: 4, directory: false }] };
  expect(parsePagedReviewResponse(directory, page)).toMatchObject(page);
  expect(() => parsePagedReviewResponse(directory, { ...page, entries: [{ ...page.entries[0], file_id: "wrong" }] })).toThrow();
  const file = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "file_content", relative_path: "notes.md", limit: 20 });
  const content = { path: "notes.md", file_id: fileId, directory_id: id, content: { text: "body", encoding: "utf8", offset: 0, next_offset: 4, size: 4, has_more: false } };
  expect(parsePagedReviewResponse(file, content)).toEqual(content);
  expect(() => parsePagedReviewResponse(file, { ...content, directory_id: "wrong" })).toThrow();
});

it("only accepts explicit runtime support for file operation admission identities", () => {
  const directory = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "workspace_files" });
  const page = { path: "/repo", entries: [] };
  expect(parsePagedReviewResponse(directory, { ...page, file_operation_identity_supported: true })).toMatchObject({ file_operation_identity_supported: true });
  expect(parsePagedReviewResponse(directory, { ...page, file_operation_identity_supported: "yes" })).toMatchObject({ file_operation_identity_supported: false });
  expect(parsePagedReviewResponse(directory, page)).not.toHaveProperty("file_operation_identity_supported", true);
});

it("follows a known file identity to its current path without accepting another generation", () => {
  const fileId = "26d9d04d-5c45-40b6-bb58-c60e7d59433c";
  const input = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", action: "file_content", file_id: fileId, relative_path: "old.md" });
  const page = { path: "renamed.md", file_id: fileId, directory_id: id, directory_revision: 4, file_identity_ambiguous: false, content: { text: "body", encoding: "utf8", offset: 0, next_offset: 4, size: 4, has_more: false } };
  expect(parsePagedReviewResponse(input, page)).toEqual(page);
  expect(() => parsePagedReviewResponse(input, { ...page, file_id: "952ccf77-3227-4579-9404-cc8db100fbe1" })).toThrow(/identity/);
  expect(() => parsePagedReviewResponse(input, { ...page, file_id: undefined })).toThrow(/identity/);
  expect(parsePagedReviewResponse(input, { ...page, file_identity_ambiguous: "invalid" })).toMatchObject({ file_identity_ambiguous: true });
});
it("rejects a response for another version", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, version_id: "b".repeat(64) })).toThrow();
});
it("rejects a file outside the selected identity", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, path: "other.txt" })).toThrow();
});
it("rejects non-progressing pagination", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, page: { ...response.page, next_line: 0 } })).toThrow();
});
it("rejects a decision without matching version and operation identity", () => {
  expect(() => pagedReviewRequestSchema.parse({ ...request, action: "approve" })).toThrow();
});

it("validates UTF8 content ranges without splitting code points", () => {
  const input = pagedReviewRequestSchema.parse({ ...request, action: "content", limit: 4 });
  const page = { version_id: id, path: "file.txt", side: "new", content: { text: "中文", encoding: "utf8", offset: 0, next_offset: 6, size: 9, has_more: true } };
  expect(parsePagedReviewResponse(input, page)).toEqual(page);
  expect(() => parsePagedReviewResponse(input, { ...page, side: "old" })).toThrow();
  expect(() => parsePagedReviewResponse(input, { ...page, content: { ...page.content, next_offset: 5 } })).toThrow();
});

it("validates shallow directory pagination and preserves older response compatibility",()=>{
 const input=pagedReviewRequestSchema.parse({task_id:"task",workspace_id:"ws",path:"/repo",action:"workspace_files",recursive:false,offset:500,limit:500});
 const page={path:"/repo",recursive:false,entries:[{path:"00-项目管理",size:0,directory:true}],next_offset:501,has_more:false};
 expect(parsePagedReviewResponse(input,page)).toMatchObject(page);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:500,has_more:true})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:undefined})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:499})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,entries:[],has_more:true})).toThrow(/pagination/);
 expect(parsePagedReviewResponse(input,{path:"/repo",entries:[]})).toMatchObject({truncated:false});
});

it("allows only read-only checkout operations with either authorized locator", () => {
  const checkout = { workspace_id: "ws", project_resource_id: "resource", path: "/repo", scope: "checkout" } as const;
  expect(pagedReviewRequestSchema.parse({ ...checkout, action: "git_history" }).scope).toBe("checkout");
  expect(pagedReviewRequestSchema.parse({ ...checkout, action: "git_manifest", git_scope: "commit", commit_sha: "1".repeat(40) }).target).toBe("");
  expect(pagedReviewRequestSchema.safeParse({ ...checkout, action: "git_manifest", git_scope: "branch" }).success).toBe(false);
  for (const action of ["stage", "commit", "submit", "merge", "versions", "manifest"]) expect(pagedReviewRequestSchema.safeParse({ ...checkout, action }).success).toBe(false);
  expect(pagedReviewRequestSchema.safeParse({ ...checkout, action: "git_manifest", git_scope: "commit", commit_sha: "main" }).success).toBe(false);
});

it("rejects a dirty or mismatched committed Git manifest", () => {
  const input = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", scope: "checkout", action: "git_manifest", git_scope: "commit", commit_sha: "1".repeat(40) });
  const manifest = { version_id: id, runtime_id: "runtime", header: { repository: "/repo", branch: "feature", target: "", head: "1".repeat(40), target_head: "", base: "", dirty: false, committed: true, commit_only: true }, page: { files: [], total_files: 0, next_offset: 0, has_more: false, additions: 0, deletions: 0 }, review: { snapshot_id: id, state: "draft", comment: "", merged_commit: "" } };
  expect(parsePagedReviewResponse(input, manifest)).toMatchObject({ header: { commit_only: true } });
  for (const header of [{ dirty: true }, { committed: false }, { commit_only: false }, { head: "2".repeat(40) }, { repository: "/other" }]) expect(() => parsePagedReviewResponse(input, { ...manifest, header: { ...manifest.header, ...header } })).toThrow(/identity/);
  expect(() => parsePagedReviewResponse(input, { ...manifest, runtime_id: "other" })).toThrow(/runtime/);
});

it("rejects nonprogressing workspace commit history", () => {
  const input = pagedReviewRequestSchema.parse({ task_id: "task", workspace_id: "ws", path: "/repo", scope: "checkout", action: "git_history" });
  expect(() => parsePagedReviewResponse(input, { commits: [], next_offset: 0, has_more: true })).toThrow(/pagination/);
});
