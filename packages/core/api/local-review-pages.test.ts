// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt" } as const;
const page = { version_id: id, path: "file.txt", preview: "text", page: { lines: [], next_line: 0, has_more: false } };

it("requests one pinned file page through the relay", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ page }), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  expect(await new ApiClient("https://api.example.test").executePagedLocalReview(request)).toEqual(page);
  expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/local-reviews/execute", expect.objectContaining({
    method: "POST", body: JSON.stringify({ task_id: "task", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt", offset: 0, limit: 100 }),
  }));
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
