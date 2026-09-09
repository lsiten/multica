// @vitest-environment node
import { expect, it, vi } from "vitest";
import { requestLocalReviewBranches } from "./local-review-request";
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "" };
it("uses the verified local runtime transport for a read-only branch action", async () => {
  const profile = { name: "desktop", port: 12345 };
  const review = vi.fn(async () => ({ branches: ["test"] }));
  expect(await requestLocalReviewBranches(request, { resolveProfile: async () => profile, health: async () => ({ profile: "desktop", workspaces: [{ id: "ws", runtimes: ["runtime"] }] }), review })).toEqual({ branches: ["test"] });
  expect(review).toHaveBeenCalledWith(profile, { ...request, action: "branches" }, undefined);
});
it("does not send a branch request to a foreign runtime", async () => {
  const review = vi.fn();
  expect(await requestLocalReviewBranches(request, { resolveProfile: async () => ({ name: "desktop", port: 12345 }), health: async () => ({ profile: "desktop", workspaces: [{ id: "ws", runtimes: ["other"] }] }), review })).toBeNull();
  expect(review).not.toHaveBeenCalled();
});
