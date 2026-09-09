// @vitest-environment jsdom

import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClient, setApiInstance } from "@multica/core/api";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { localReviewInventory } from "../../platform/local-review";
import { readReviewManifest, readReviewFile } from "../../platform/local-review-pages";
import { reviewFileFixture, reviewManifestFixture } from "../../test/local-review-pages";
import { issueKeys } from "@multica/core/issues/queries";
import type { AgentTask } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { CodeReviewContextSection } from "./code-review-context-section";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => { const state = { user: { id: "viewer" } }; return { useAuthStore: Object.assign((select: (value: typeof state) => unknown) => select(state), { getState: () => state }) }; });
vi.mock("../../platform/local-review", () => ({ readLocalReview: vi.fn(), readLocalReviewBranches: async () => ["main"], localReviewInventory: vi.fn() }));
vi.mock("../../platform/local-review-pages", () => ({ readReviewManifest: vi.fn(), readReviewFile: vi.fn(), readReviewRepositories: async (input: PagedReviewInput) => ({ repositories: [input.path] }), readReviewCommits: vi.fn(), renewReviewLease: async (input: PagedReviewInput) => ({ version_id: input.version_id || "", expires_at: "2100-01-01T00:00:00Z" }) }));

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

describe("CodeReviewContextSection", () => {
  afterEach(cleanup);
  beforeEach(() => {
    vi.mocked(localReviewInventory).mockResolvedValue([{
      taskId: task.id, workspaceId: "ws-1", runtimeId: "runtime-1", agentId: "agent-1",
      path: "/managed/review-worktree", taskName: "agent/review-123", repositories: ["/managed/review-worktree"], active: false,
    }]);
  });
  it("does not offer a historical path when the runtime inventory has no repository", async () => {
    class FixtureClient extends ApiClient { override async listTasksByIssue() { return [task]; } }
    setApiInstance(new FixtureClient("https://fixture.invalid"));
    vi.mocked(localReviewInventory).mockResolvedValue([]);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(issueKeys.tasks("issue-1"), [task]);
    renderWithI18n(<QueryClientProvider client={queryClient}><CodeReviewContextSection issueId="issue-1" /></QueryClientProvider>, { locale: "zh-Hans" });
    await waitFor(() => expect(localReviewInventory).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "查看 / 提交 MR" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
  it("hides finalized runs even when an old inventory still contains their path", async () => {
    class FixtureClient extends ApiClient { override async listTasksByIssue() { return [{ ...task, durable_work_dir: "/delivery" }]; } }
    setApiInstance(new FixtureClient("https://fixture.invalid"));
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(issueKeys.tasks("issue-1"), [{ ...task, durable_work_dir: "/delivery" }]);
    renderWithI18n(<QueryClientProvider client={queryClient}><CodeReviewContextSection issueId="issue-1" /></QueryClientProvider>, { locale: "zh-Hans" });
    await waitFor(() => expect(localReviewInventory).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "查看 / 提交 MR" })).not.toBeInTheDocument();
  });
  it("opens the local MR for the same run whose branch and path are shown", async () => {
    class FixtureClient extends ApiClient { override async listTasksByIssue() { return [task]; } }
    const api = new FixtureClient("https://fixture.invalid");
    const createComment = vi.spyOn(api, "createComment").mockRejectedValue(new Error("Unexpected comment request"));
    setApiInstance(api);
    vi.mocked(readReviewManifest).mockImplementation(async (input) => reviewManifestFixture(input));
    vi.mocked(readReviewFile).mockImplementation(async (input) => reviewFileFixture(input, "+entry-change"));
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    queryClient.setQueryData(issueKeys.tasks("issue-1"), [task]);

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <CodeReviewContextSection issueId="issue-1" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    expect(screen.getByText("本地 MR")).toBeInTheDocument();
    expect(await screen.findByText("agent/review-123")).toBeInTheDocument();
    expect(screen.getByText("/managed/review-worktree")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "查看 / 提交 MR" }));
    expect(await screen.findByText("+entry-change")).toBeInTheDocument();
    expect(readReviewManifest).toHaveBeenCalledWith(expect.objectContaining({ task_id: "task-1", runtime_id: "runtime-1", path: "/managed/review-worktree" }), expect.any(AbortSignal));
    fireEvent.click(screen.getByRole("button", { name: "确认通过" }));
    await waitFor(() => expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ task_id: "task-1", action: "approve", snapshot_id: "a".repeat(64) })));
    expect(createComment).not.toHaveBeenCalled();
  });
});
