import { expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import type { AnchorHTMLAttributes } from "react";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";
import { renderWithI18n } from "../../test/i18n";
import { RuntimeEnvironmentResources } from "./runtime-environment-resources";

vi.mock("../../navigation", () => ({ AppLink: (props: AnchorHTMLAttributes<HTMLAnchorElement>) => <a {...props} /> }));

it("separates physical code from historical runs and links the task retaining it", () => {
  const rows = parseManagedWorktrees([
    { workspace_id: "ws", task_short: "delivery", path: "/code", kind: "issue", size_bytes: 1, active: false, environment_kind: "git_worktree", code_environment_id: "code", physical_worktree_ids: ["git-dir"], retention_reason: "task_review", retained_task_id: "current-run", issue_id: "issue", issue_status: "in_review", run_status: "completed" },
    { workspace_id: "ws", task_short: "old-run", path: "/run", kind: "issue", size_bytes: 1, active: false, environment_kind: "run_directory", code_environment_id: "code", physical_worktree_ids: [], retention_reason: "" },
  ]);
  const { container } = renderWithI18n(<RuntimeEnvironmentResources workspaceSlug="workspace" rows={rows} />, { locale: "zh-Hans" });
  const metrics = container.querySelector("dl")!;
  expect(within(metrics).getByText("物理检出").parentElement).toHaveTextContent("1");
  expect(within(metrics).getByText("运行记录").parentElement).toHaveTextContent("1");
  expect(within(metrics).getByText("待回收").parentElement).toHaveTextContent("1");
  expect(screen.getByText("任务待审核")).toBeInTheDocument();
  expect(screen.getByText("没有当前任务支撑，等待删除")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "对应任务：issue" })).toHaveAttribute("href", "/workspace/issues/issue");
});
