// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseManagedWorktrees, parseManagedWorktreeCleanup } from "./managed-worktree";

describe("worktree response boundary", () => {
  const row = { workspace_id: "ws", task_short: "run", path: "/managed/run", kind: "issue", size_bytes: 12 };
  it("keeps old daemon entries protected until explicit safety fields arrive", () => {
    expect(parseManagedWorktrees([row])[0]).toMatchObject({ workspaceId: "ws", active: true, protectionReason: "unavailable" });
  });
  it("converts the wire contract to camelCase", () => {
    expect(parseManagedWorktrees([{ ...row, active: false, protection_reason: "", agent_id: "a" }])[0]).toMatchObject({ agentId: "a", active: false, protectionReason: "" });
    expect(parseManagedWorktreeCleanup({ removed_paths: [row.path], retained: {} })).toEqual({ removedPaths: [row.path], retained: {} });
  });
  it("does not turn malformed inventories or uncertain deletion responses into success", () => {
    expect(() => parseManagedWorktrees([{ ...row, path: 12 }])).toThrow();
    expect(() => parseManagedWorktrees(null)).toThrow();
    expect(() => parseManagedWorktreeCleanup({ removed_paths: null })).toThrow();
  });
});
