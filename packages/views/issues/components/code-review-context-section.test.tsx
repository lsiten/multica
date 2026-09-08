// @vitest-environment jsdom

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { setApiInstance } from "@multica/core/api";
import type { ApiClient } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import type { AgentTask } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { CodeReviewContextSection } from "./code-review-context-section";

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
  it("shows the task branch and worktree before requesting an agent review", async () => {
    const createComment = vi.fn().mockResolvedValue({ id: "comment-1" });
    setApiInstance({ createComment } as unknown as ApiClient);
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

    expect(screen.getByText("代码上下文")).toBeInTheDocument();
    expect(screen.getByText("agent/review-123")).toBeInTheDocument();
    expect(screen.getByText("/managed/review-worktree")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Review 代码改动" }));
    await waitFor(() => {
      expect(createComment).toHaveBeenCalledWith(
        "issue-1",
        expect.stringContaining("mention://agent/agent-1"),
      );
    });
  });
});
