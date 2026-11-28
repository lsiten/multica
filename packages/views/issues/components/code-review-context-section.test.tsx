// @vitest-environment jsdom
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import type { AgentTask } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { CodeReviewContextSection } from "./code-review-context-section";
import { readLocalIndex } from "../../platform/local-review-pages";
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/api", () => ({ api: { listTasksByIssue: vi.fn() } }));
vi.mock("../../platform/local-review-pages", () => ({ readReviewRepositories: async (input: { path: string }) => ({ repositories: [input.path] }), readGitBranches: async () => ({ branches: ["main"] }), readLocalIndex: vi.fn() }));
const task: AgentTask = {
  id: "task-1",
  agent_id: "agent-1",
  runtime_id: "runtime-1",
  issue_id: "issue-1",
  status: "completed",
  priority: 0,
  dispatched_at: null,
  started_at: "2026-09-08T01:00:00Z",
  completed_at: "2026-09-08T01:05:00Z",
  result: null,
  error: null,
  created_at: "2026-09-08T01:00:00Z",
  work_dir: "/managed/review-worktree",
  branch_name: "agent/review-123",
};

afterEach(cleanup);
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(readLocalIndex).mockResolvedValue({ kind: "index", version_id: "a".repeat(64), status: { index_id: "b".repeat(64), branch: "feature", head: "1".repeat(40), files: [] } });
});
it("deduplicates shared directories while preserving runtime ownership", async () => {
  vi.mocked(api.listTasksByIssue).mockResolvedValue([task, { ...task, id: "older", created_at: "2026-09-07T00:00:00Z" }, { ...task, id: "remote", runtime_id: "runtime-2" }]);
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><CodeReviewContextSection issueId="issue-1" /></QueryClientProvider>);
  const selector = await screen.findByRole("combobox", { name: "Directory" });
  expect(within(selector).getAllByRole("option")).toHaveLength(2);
  fireEvent.change(selector, { target: { value: "remote" } });
  fireEvent.click(screen.getByRole("button", { name: "View Git changes" }));
  expect(await within(screen.getByRole("dialog")).findByText("runtime-2")).toBeInTheDocument();
  expect(readLocalIndex).toHaveBeenCalledWith(expect.objectContaining({ task_id: "remote", runtime_id: "runtime-2", path: task.work_dir, scope: "checkout" }), expect.any(AbortSignal));
  expect(screen.getByRole("button", { name: "Uncommitted" })).toHaveAttribute("aria-pressed", "true");
});
it("keeps the stored workspace path rather than substituting an archive", async () => {
  vi.mocked(api.listTasksByIssue).mockResolvedValue([{ ...task, durable_work_dir: "/archive" }]);
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><CodeReviewContextSection issueId="issue-1" /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "View Git changes" }));
  expect(await within(screen.getByRole("dialog")).findByText(task.work_dir ?? "")).toBeInTheDocument();
  expect(screen.queryByText("/archive")).not.toBeInTheDocument();
});
