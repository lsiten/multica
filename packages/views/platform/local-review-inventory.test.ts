import { afterEach, expect, it, vi } from "vitest";
import { localReviewInventoryPage } from "./local-review";

const api = vi.hoisted(() => ({ listReviewWorktrees: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => vi.unstubAllGlobals());

it("clears missing and context-only local repositories without truncating the page or filtering foreign runtimes", async () => {
  const row = { workspaceId: "ws", taskId: "task", runtimeId: "local", agentId: "agent", path: "/root/gone", taskName: "task", repositories: ["/root/gone"], active: false };
  api.listReviewWorktrees.mockResolvedValue([row, { ...row, path: "/root/context", repositories: ["/root/context"] }, { ...row, path: "/root/live", repositories: ["/root/live/repo"] }, { ...row, runtimeId: "remote" }]);
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => ({
    health: { profile: "desktop", workspaces: [{ id: "ws", runtimes: ["local"] }] },
    worktrees: [
      { workspace_id: "ws", task_id: "task", task_short: "task", path: "/root/context", kind: "task", size_bytes: 0, repositories: [] },
      { workspace_id: "ws", task_id: "task", task_short: "task", path: "/root/live", kind: "task", size_bytes: 1, repositories: ["/root/live/repo"] },
    ],
  }) });
  const result = await localReviewInventoryPage(0);
  expect(result.map((entry) => entry.repositories)).toEqual([[], [], ["/root/live/repo"], ["/root/gone"]]);
});

it("surfaces unavailable local inventory instead of reporting deletion", async () => {
  api.listReviewWorktrees.mockResolvedValue([]);
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => { throw new Error("inventory unavailable"); } });
  await expect(localReviewInventoryPage(0)).rejects.toThrow("inventory unavailable");
});

it("rejects malformed IPC inventory instead of treating it as a cleared directory", async () => {
  api.listReviewWorktrees.mockResolvedValue([]);
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => ({ health: { profile: "desktop", workspaces: [] }, worktrees: null }) });
  await expect(localReviewInventoryPage(0)).rejects.toThrow("Invalid worktree inventory response");
});

it("does not use a foreign runtime repository to revive a missing local worktree", async () => {
  api.listReviewWorktrees.mockResolvedValue([{ workspaceId: "ws", taskId: "task", runtimeId: "local", agentId: "agent", path: "/root/gone", taskName: "task", repositories: ["/root/gone"], active: false }]);
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => ({
    health: { profile: "desktop", workspaces: [{ id: "ws", runtimes: ["local"] }] },
    worktrees: [{ workspace_id: "ws", runtime_id: "foreign", task_short: "task", path: "/root/gone", kind: "task", size_bytes: 1, repositories: ["/root/gone"] }],
  }) });
  const result = await localReviewInventoryPage(0);
  expect(result[0]?.repositories).toEqual([]);
});
