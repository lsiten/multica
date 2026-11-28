// @vitest-environment node
import { expect, it, vi } from "vitest";
import { requestLocalReviewPage } from "./local-review-request";

const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main", action: "file", version_id: "a".repeat(64), file_path: "file.txt" };
const profile = { name: "desktop", port: 12345 };
const health = { profile: "desktop", local_review_paging_supported: true, workspaces: [{ id: "ws", runtimes: ["runtime"] }] };
const response = { version_id: request.version_id, path: "file.txt", preview: "text", page: { lines: [], next_line: 0, has_more: false } };

it("uses a single verified profile for paging", async () => {
  const review = vi.fn().mockResolvedValue(response);
  expect(await requestLocalReviewPage(request, { resolveProfile: async () => profile, health: async () => health, review })).toMatchObject({ ...response, runtime_id: "runtime" });
  expect(review).toHaveBeenCalledWith(profile, expect.objectContaining({ action: "file", runtime_id: "runtime" }), undefined);
});
it("returns null without dispatching to a foreign runtime", async () => {
  const review = vi.fn();
  expect(await requestLocalReviewPage(request, { resolveProfile: async () => profile, health: async () => ({ ...health, workspaces: [] }), review })).toBeNull();
  expect(review).not.toHaveBeenCalled();
});
it("rejects an old owning daemon before dispatch", async () => {
  const review = vi.fn();
  await expect(requestLocalReviewPage(request, { resolveProfile: async () => profile, health: async () => ({ ...health, local_review_paging_supported: false }), review })).rejects.toThrow("local_review_paging_upgrade_required");
  expect(review).not.toHaveBeenCalled();
});

it("preserves cancellation through the owned daemon request", async () => {
  const controller = new AbortController();
  const review = vi.fn().mockResolvedValue(response);
  await requestLocalReviewPage(request, { resolveProfile: async () => profile, health: async () => health, review }, controller.signal);
  expect(review).toHaveBeenCalledWith(profile, expect.objectContaining({ task_id: "task" }), controller.signal);
});

it("does not dispatch after cancellation during profile selection", async () => {
  const controller = new AbortController();
  const review = vi.fn();
  const healthRead = vi.fn();
  await expect(requestLocalReviewPage(request, {
    resolveProfile: async () => { controller.abort(); return profile; },
    health: healthRead,
    review,
  }, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(healthRead).not.toHaveBeenCalled();
  expect(review).not.toHaveBeenCalled();
});

it("requires separate index support even when paging is available", async () => {
  const review = vi.fn();
  await expect(requestLocalReviewPage({ ...request, action: "index" }, { resolveProfile: async () => profile, health: async () => health, review })).rejects.toThrow("local_review_index_upgrade_required");
  expect(review).not.toHaveBeenCalled();
});

it("keeps checkout project and unresolved runtime authorization on the server", async () => {
  const review = vi.fn();
  const transport = { resolveProfile: async () => profile, health: async () => health, review };
  expect(await requestLocalReviewPage({ task_id: "task", workspace_id: "ws", path: "/repo", scope: "checkout", action: "branches" }, transport)).toBeNull();
  expect(await requestLocalReviewPage({ project_resource_id: "resource", workspace_id: "ws", runtime_id: "runtime", path: "/repo", scope: "checkout", action: "branches" }, transport)).toBeNull();
  expect(review).not.toHaveBeenCalled();
});

it("preserves checkout scope and known owning runtime for branch reads", async () => {
  const review = vi.fn().mockResolvedValue({ branches: ["refs/heads/main"] });
  expect(await requestLocalReviewPage({ ...request, action: "branches", scope: "checkout", version_id: undefined }, { resolveProfile: async () => profile, health: async () => health, review })).toMatchObject({ branches: ["refs/heads/main"] });
  expect(review).toHaveBeenCalledWith(profile, expect.objectContaining({ scope: "checkout", task_id: "task", runtime_id: "runtime" }), undefined);
});
