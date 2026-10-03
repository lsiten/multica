// @vitest-environment node
import { expect, it } from "vitest";
import { parseManagedWorktrees } from "./managed-worktree";
import { emptyWorktreeFilters, filterWorktreeInventory } from "./worktree-filters";

it("combines workspace, project, squad and runtime activity without guessing missing bindings", () => {
  const rows = parseManagedWorktrees([
    { workspace_id: "ws", agent_id:"agent", task_short: "自动化", path: "/first", kind: "autopilot_run", size_bytes: 0, project_id: "project", squad_id: "squad", environment_kind: "directory", active: false, protection_reason: "output" },
    { workspace_id: "other", task_short: "自动化", path: "/second", kind: "autopilot_run", size_bytes: 0, project_id: "project", squad_id: "squad", environment_kind: "directory", active: false },
    { workspace_id: "ws", task_short: "任务", path: "/legacy", kind: "issue", size_bytes: 0 },
  ]);
  expect(filterWorktreeInventory(rows, { ...emptyWorktreeFilters, workspace: "ws", project: "project", squad: "squad", environment: "directory", kind: "autopilot_run", activity: "inactive", search: "自动化" }).map((row) => row.path)).toEqual(["/first"]);
  expect(filterWorktreeInventory(rows, { ...emptyWorktreeFilters, workspace: "ws" }).map((row) => row.path)).toEqual(["/first", "/legacy"]);
  expect(filterWorktreeInventory(rows, { ...emptyWorktreeFilters, activity: "protected", search: "FIRST" }).map((row) => row.path)).toEqual(["/first"]);
  expect(filterWorktreeInventory(rows,{...emptyWorktreeFilters,agent:"agent"}).map((row)=>row.path)).toEqual(["/first"]);
  expect(filterWorktreeInventory(rows,{...emptyWorktreeFilters,agent:"other"})).toEqual([]);
});
