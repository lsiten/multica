// @vitest-environment node
import { expect, it } from "vitest";
import { compareWorktreeLifecycle, toWorktreeLifecycle, worktreeLifecycleSchema } from "./worktree-lifecycle";
import { remoteWorktreesSchema } from "./local-review";

it("puts stale work before recent work and orders each action by oldest activity", () => {
  const rows = [
    { issue_id: "recent", next_action: "review", last_activity_at: "2026-09-27T00:00:00Z" },
    { issue_id: "older", next_action: "review", last_activity_at: "2026-09-26T00:00:00Z" },
    { issue_id: "stale", next_action: "retained", stale: true, last_activity_at: "2026-09-01T00:00:00Z" },
  ].map((row) => toWorktreeLifecycle(worktreeLifecycleSchema.parse(row)));
  expect(rows.toSorted(compareWorktreeLifecycle).map((row) => row.issueId)).toEqual(["stale", "older", "recent"]);
});

it("keeps quick-create runs without an issue in mixed old and new server inventories", () => {
  const row = { workspace_id: "ws", task_id: "task", runtime_id: "runtime", agent_id: "agent", work_dir: "/repo", status: "completed", branch_name: null };
  const inventory = remoteWorktreesSchema.parse([
    { ...row, issue_id: null },
    { ...row, task_id: "linked", issue_id: "issue", run_status: "completed", next_action: "unknown", repositories_details: [], completed_at: null },
  ]);
  expect(inventory.map((entry) => entry.issueId)).toEqual(["", "issue"]);
  expect(inventory.map((entry) => entry.nextAction)).toEqual(["unknown", "unknown"]);
});
