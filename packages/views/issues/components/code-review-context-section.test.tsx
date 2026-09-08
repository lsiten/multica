// @vitest-environment jsdom

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiClient, setApiInstance } from "@multica/core/api";
import type { LocalReviewSnapshot } from "@multica/core/types/local-review";
import { readLocalReview } from "../../platform/local-review";
import { issueKeys } from "@multica/core/issues/queries";
import type { AgentTask } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { CodeReviewContextSection } from "./code-review-context-section";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../platform/local-review", () => ({ readLocalReview: vi.fn(), localReviewInventory: async () => [] }));

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
  it("opens the local MR for the same run whose branch and path are shown", async () => {
    class FixtureClient extends ApiClient { override async listTasksByIssue() { return [task]; } }
    const api = new FixtureClient("https://fixture.invalid");
    const createComment = vi.spyOn(api, "createComment").mockRejectedValue(new Error("Unexpected comment request"));
    setApiInstance(api);
    vi.mocked(readLocalReview).mockImplementation(async (request): Promise<LocalReviewSnapshot> => ({
      id: "entry-snapshot", path: request.path, branch: "agent/review-123", target: request.target,
      head: "head", target_head: "base", base: "base", dirty: false, repositories: [], branches: ["main"], commits: "head change",
      files: [{ path: "entry.ts", status: "tracked", patch: "@@ -1 +1 @@\n-before\n+entry-change" }],
      review: { snapshot_id: "entry-snapshot", state: request.action === "approve" ? "approved" : "open", comment: "", merged_commit: "" },
    }));
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
    expect(screen.getByText("agent/review-123")).toBeInTheDocument();
    expect(screen.getByText("/managed/review-worktree")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "查看 / 提交 MR" }));
    expect(await screen.findByText("+entry-change")).toBeInTheDocument();
    expect(readLocalReview).toHaveBeenCalledWith(expect.objectContaining({ task_id: "task-1", runtime_id: "runtime-1", path: "/managed/review-worktree" }));
    fireEvent.click(screen.getByRole("button", { name: "确认通过" }));
    await waitFor(() => expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ task_id: "task-1", action: "approve", snapshot_id: "entry-snapshot" })));
    expect(createComment).not.toHaveBeenCalled();
  });
});
