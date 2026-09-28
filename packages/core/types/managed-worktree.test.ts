// @vitest-environment node
import { describe, expect, it } from "vitest";
import { canCleanWorktree, canDiscardWorktree, parseManagedWorktrees, parseManagedWorktreeCleanup } from "./managed-worktree";

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
  it.each([undefined, "future_action"])("protects legacy and unknown lifecycle actions: %s", (nextAction) => {
    const entries = parseManagedWorktrees([{ ...row, active: false, protection_reason: "", run_status: "completed", next_action: nextAction }]);
    expect(entries.map(canCleanWorktree)).toEqual([false]);
    expect(entries.map(canDiscardWorktree)).toEqual([false]);
  });
  it("separates safe batch cleanup from explicit terminal worktree discard", () => {
    const entries = parseManagedWorktrees([
      { ...row, active: false, protection_reason: "", run_status: "completed", next_action: "cleanup" },
      { ...row, active: false, protection_reason: "dirty", run_status: "failed", next_action: "retained" },
      { ...row, active: true, protection_reason: "dirty", run_status: "completed", next_action: "retained" },
      { ...row, active: false, protection_reason: "review", run_status: "completed", next_action: "cleanup" },
      { ...row, active: false, protection_reason: "", run_status: "future_status", next_action: "cleanup" },
    ]);
    expect(entries.map(canCleanWorktree)).toEqual([true, false, false, false, false]);
    expect(entries.map(canDiscardWorktree)).toEqual([false, true, false, false, false]);
  });
  it("preserves lifecycle evidence and rejects invalid timestamps from display", () => {
    const entries = parseManagedWorktrees([{ ...row, run_status: "completed", issue_id: "issue", issue_status: "in_review", completed_at: "bad", last_activity_at: "2026-09-01T00:00:00Z", stale: true, next_action: "merge", repositories_details: [{ path: "/repo", target: "release", review_state: "approved", next_action: "merge", reason: "approved" }] }]);
    expect(entries[0]).toMatchObject({ runStatus: "completed", issueId: "issue", issueStatus: "in_review", completedAt: undefined, lastActivityAt: "2026-09-01T00:00:00Z", stale: true, nextAction: "merge", repositoryDetails: [{ path: "/repo", target: "release", reviewState: "approved", nextAction: "merge" }] });
  });
});
