// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { z } from "zod";

afterEach(() => vi.unstubAllGlobals());
const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt" } as const;
const page = { version_id: id, path: "file.txt", preview: "text", page: { lines: [], next_line: 0, has_more: false } };

it("reads the same file at its moved path through the HTTP client's identity contract", async () => {
  const fileId = "66f46490-15d2-43ca-bdbb-89a18617dd9e";
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (typeof init?.body !== "string") throw new Error("Missing JSON request");
    const input = z.object({ file_id: z.string().optional() }).parse(JSON.parse(init.body));
    if (input.file_id !== fileId) return new Response(JSON.stringify({ error: "Old path missing" }), { status: 404 });
    return new Response(JSON.stringify({ page: { file_id: fileId, path: "moved.md", content: { text: "Retained body", encoding: "utf8", offset: 0, next_offset: 13, size: 13, has_more: false } } }), { status: 200 });
  }));

  const result = await new ApiClient("https://api.example.test").executePagedLocalReview({ task_id: "task", workspace_id: "ws", path: "/root", action: "file_content", relative_path: "old.md", file_id: fileId });

  expect(result).toMatchObject({ file_id: fileId, path: "moved.md", content: { text: "Retained body" } });
});

it("preserves a file operation and its receipt across the HTTP relay contract", async () => {
  let body: unknown;
  const receipt = { kind: "file_operation", command_id: "create-once", operation: "create_file", path: "created.md", directory: false };
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (typeof init?.body !== "string") throw new Error("Missing JSON request");
    body = JSON.parse(init.body);
    return new Response(JSON.stringify({ page: receipt }), { status: 200 });
  }));
  const result = await new ApiClient("https://api.example.test").executePagedLocalReview({ task_id: "task", workspace_id: "ws", path: "/root", action: "file_operation", command_id: "create-once", file_operation: { kind: "create_file", path: "created.md", content: "new body" } });
  expect(body).toMatchObject({ action: "file_operation", command_id: "create-once", file_operation: { kind: "create_file", path: "created.md", content: "new body" } });
  expect(result).toEqual(receipt);
});

it("forwards the project resource and relative file path for taskless directory previews", async () => {
  let body: unknown;
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (typeof init?.body !== "string") throw new Error("Missing JSON request");
    body = JSON.parse(init.body);
    return new Response(JSON.stringify({ page: { path: "src/app.ts", content: { text: "hello", encoding: "utf8", offset: 0, next_offset: 5, size: 5, has_more: false } } }), { status: 200 });
  }));

  await new ApiClient("https://api.example.test").executePagedLocalReview({ workspace_id: "ws", project_resource_id: "resource", runtime_id: "runtime", path: "/project", action: "file_content", relative_path: "src/app.ts" });

  expect(body).toMatchObject({ task_id: "", project_resource_id: "resource", runtime_id: "runtime", relative_path: "src/app.ts", action: "file_content" });
});

it("requests one pinned file page through the relay", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ page }), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  expect(await new ApiClient("https://api.example.test").executePagedLocalReview(request)).toEqual(page);
  expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/local-reviews/execute", expect.objectContaining({
    method: "POST", body: JSON.stringify({ task_id: "task", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt", offset: 0, limit: 100 }),
  }));
});

it("requests saved review versions without a target branch", async () => {
  const versions = { versions: [{ version_id: id, updated_at: "2026-11-16T10:00:00.000Z", header: { repository: "/repo", branch: "feature", target: "main", head: "b".repeat(40), target_head: "c".repeat(40), base: "d".repeat(40), dirty: true, committed: false } }] };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ page: versions }), { status: 200 })));
  const result = await new ApiClient("https://api.example.test").executePagedLocalReview({ task_id: "task", workspace_id: "ws", path: "/repo", action: "versions" });
  expect(result).toEqual(versions);
});

it("forwards the explicit review scope without changing legacy payloads", async () => {
  let body: unknown;
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    body = JSON.parse(String(init?.body));
    return new Response(JSON.stringify({ page }), { status: 200 });
  }));
  await new ApiClient("https://api.example.test").executePagedLocalReview({ ...request, scope: "run" });
  expect(body).toMatchObject({ scope: "run", task_id: "task" });
});

it("rejects a mismatched page returned by the relay", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ page: { ...page, version_id: "b".repeat(64) } }), { status: 200 })));
  await expect(new ApiClient("https://api.example.test").executePagedLocalReview(request)).rejects.toThrow("Review version mismatch");
});

it.each([{}, { local_review_supported: true }, { local_review_paging_supported: false }])("does not mistake legacy capabilities for paging support", async (config) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(config), { status: 200 })));
  expect(await new ApiClient("https://api.example.test").supportsPagedLocalMR()).toBe(false);
});

it.each(["capability", "page"] as const)("propagates reader cancellation into the %s HTTP request", async (kind) => {
  const controller = new AbortController();
  let transportSignal: AbortSignal | null | undefined;
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    transportSignal = init?.signal;
    return new Response(JSON.stringify(kind === "page" ? { page } : { local_review_paging_supported: true }), { status: 200 });
  }));
  const client = new ApiClient("https://api.example.test");
  if (kind === "page") await client.executePagedLocalReview(request, controller.signal);
  else await client.supportsPagedLocalMR(controller.signal);
  controller.abort();
  expect(transportSignal?.aborted).toBe(true);
});

it("propagates cancellation to branch discovery without changing its payload", async () => {
  const controller = new AbortController();
  let transportSignal: AbortSignal | null | undefined;
  const fetchMock = vi.fn(async (_url: unknown, init?: RequestInit) => {
    transportSignal = init?.signal;
    return new Response(JSON.stringify({ branches: ["main"] }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new ApiClient("https://api.example.test");
  await client.listLocalReviewBranches({ task_id: "task", workspace_id: "ws", path: "/repo", target: "main" }, controller.signal);
  controller.abort();
  expect(transportSignal?.aborted).toBe(true);
  expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/local-reviews/execute", expect.objectContaining({ body: JSON.stringify({ task_id: "task", path: "/repo", action: "branches" }) }));
});

it("sends the selected index identity and file set through the relay", async () => {
  let body: unknown;
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (typeof init?.body !== "string") throw new Error("Missing JSON request");
    body = JSON.parse(init.body);
    return new Response(JSON.stringify({ page: { kind: "index_result", result: { index_id: "e".repeat(64) } } }), { status: 200 });
  }));
  await new ApiClient("https://api.example.test").executePagedLocalReview({ ...request, action: "stage", version_id: id, snapshot_id: id, index_id: "b".repeat(64), head: "c".repeat(40), branch: "feature", paths: ["selected.ts"], command_id: "stage-once" });
  expect(body).toMatchObject({ action: "stage", command_id: "stage-once", index_id: "b".repeat(64), head: "c".repeat(40), branch: "feature", paths: ["selected.ts"] });
});

it("preserves shallow directory mode and its cursor through the relay",async()=>{
 let body:unknown;
 vi.stubGlobal("fetch",vi.fn(async(_url:unknown,init?:RequestInit)=>{
  body=JSON.parse(String(init?.body));
  return new Response(JSON.stringify({page:{path:"/project",recursive:false,entries:[],next_offset:500,has_more:false}}),{status:200});
 }));
 await new ApiClient("https://api.example.test").executePagedLocalReview({workspace_id:"ws",project_resource_id:"resource",path:"/project",action:"workspace_files",recursive:false,relative_path:".",offset:500,limit:500});
 expect(body).toMatchObject({recursive:false,relative_path:".",offset:500,limit:500});
});

it("sends the selected checkout commit through the HTTP relay", async () => {
  const sha = "1".repeat(40);
  const manifest = { version_id: id, header: { repository: "/repo", branch: "feature", target: "", head: sha, target_head: "", base: "", dirty: false, committed: true, commit_only: true }, page: { files: [], total_files: 0, next_offset: 0, has_more: false, additions: 0, deletions: 0 }, review: { snapshot_id: id, state: "draft", comment: "", merged_commit: "" } };
  vi.stubGlobal("fetch", vi.fn(async (_url: unknown, init?: RequestInit) => {
    const input = z.object({ scope: z.literal("checkout"), git_scope: z.literal("commit"), commit_sha: z.literal(sha), project_resource_id: z.literal("resource") }).parse(JSON.parse(String(init?.body)));
    return new Response(JSON.stringify({ page: { ...manifest, header: { ...manifest.header, head: input.commit_sha } } }), { status: 200 });
  }));
  expect(await new ApiClient("https://api.example.test").executePagedLocalReview({ workspace_id: "ws", project_resource_id: "resource", path: "/repo", scope: "checkout", action: "git_manifest", git_scope: "commit", commit_sha: sha })).toMatchObject({ header: { head: sha } });
});

it("parses checkout refs from the branch relay envelope", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ branches: ["refs/heads/main", "refs/remotes/origin/main"] }), { status: 200 })));
  expect(await new ApiClient("https://api.example.test").executePagedLocalReview({ task_id: "task", workspace_id: "ws", path: "/repo", scope: "checkout", action: "branches" })).toEqual({ branches: ["refs/heads/main", "refs/remotes/origin/main"] });
});
