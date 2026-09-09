// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

it("fetches branch choices without requesting a diff or target", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ branches: ["test", "release"] }), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  expect(await new ApiClient("https://api.example.test").listLocalReviewBranches(request)).toEqual(["test", "release"]);
  expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/local-reviews/execute", expect.objectContaining({ body: JSON.stringify({ task_id: "task", path: "/runtime/repo", action: "branches" }) }));
});

it("rejects malformed branch-list responses", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ branches: [false] }), { status: 200 })));
  await expect(new ApiClient("https://api.example.test").listLocalReviewBranches(request)).rejects.toThrow();
});

const request = { task_id: "task", workspace_id: "workspace", path: "/runtime/repo", target: "main", action: "approve", snapshot_id: "snapshot", comment: "reviewed" } as const;
const response = {
  snapshot: { id: "snapshot", path: "/runtime/repo", branch: "feature", target: "main", head: "head", target_head: "target", base: "base", dirty: false, branches: ["main", "feature"], commits: "", files: [] },
  review: { snapshot_id: "snapshot", state: "approved", comment: "reviewed", merged_commit: "", events: [{ kind: "approve", snapshot_id: "snapshot", comment: "reviewed", actor_id: "user", created_at: "2026-09-08T00:00:00Z" }] },
};

it("forwards one action and reads its runtime-owned approval history", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  const client = new ApiClient("https://api.example.test");
  const result = await client.executeLocalReview(request);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(fetchMock).toHaveBeenCalledWith("https://api.example.test/api/local-reviews/execute", expect.objectContaining({
    method: "POST", body: JSON.stringify({ task_id: "task", path: "/runtime/repo", target: "main", action: "approve", snapshot_id: "snapshot", comment: "reviewed" }),
  }));
  expect(result.review.state).toBe("approved");
  expect(result.history[0]?.actor_id).toBe("user");
  expect(result.repositories).toEqual([]);
});

it.each([
  { ...response, review: null },
  { ...response, snapshot: { ...response.snapshot, files: "invalid" } },
  { ...response, review: { ...response.review, state: "unknown" } },
])("rejects malformed runtime review data", async (malformed) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(malformed), { status: 200 })));
  await expect(new ApiClient("https://api.example.test").executeLocalReview(request)).rejects.toThrow();
});
