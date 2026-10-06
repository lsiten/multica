// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseManagedWorktrees } from "./managed-worktree";
import { summarizeWorktreeInventory } from "./worktree-inventory";

describe("worktree inventory", () => {
  it("counts shared code once without hiding historical runs or pending cleanup", () => {
    const rows = parseManagedWorktrees([
      { workspace_id: "ws", task_short: "first", path: "/code", kind: "issue", size_bytes: 1, active: false, environment_kind: "git_worktree", code_environment_id: "code", physical_worktree_ids: ["git-dir"], retention_reason: "task_review", retained_task_id: "current", issue_id: "issue" },
      { workspace_id: "ws", task_short: "second", path: "/run", kind: "issue", size_bytes: 1, active: false, environment_kind: "run_directory", code_environment_id: "code", physical_worktree_ids: [], retention_reason: "" },
    ]);
    expect(summarizeWorktreeInventory(rows)).toEqual({ physicalWorktrees: 1, codeEnvironments: 1, tasks: 1, runRecords: 1, pendingCleanup: 1, unresolved: 0 });
  });
  it("does not treat a legacy response as verified or manufacture a physical worktree", () => {
    const rows = parseManagedWorktrees([{ workspace_id: "ws", task_short: "old", path: "/old", kind: "issue", size_bytes: 1 }]);
    expect(summarizeWorktreeInventory(rows)).toMatchObject({ physicalWorktrees: 0, pendingCleanup: 0, unresolved: 1 });
  });
});
