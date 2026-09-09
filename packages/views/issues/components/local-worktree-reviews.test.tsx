// @vitest-environment jsdom
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import { LocalWorktreeReviews } from "./local-worktree-reviews";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { reviewFileFixture, reviewManifestFixture } from "../../test/local-review-pages";

vi.mock("@multica/core/auth", () => ({ useAuthStore: (select: (state: { user: { id: string } }) => unknown) => select({ user: { id: "viewer" } }) }));
vi.mock("../../platform/local-review", () => ({ readLocalReviewBranches: async () => ["main"], localReviewInventoryPage: async () => [
  { taskId: "task-a", workspaceId: "ws", runtimeId: "runtime-a", agentId: "agent-a", taskName: "feature-a", repositories: ["/remote/worktree-a"] },
  { taskId: "task-b", workspaceId: "ws", runtimeId: "runtime-b", agentId: "agent-b", taskName: "feature-b", repositories: ["/remote/worktree-b"] },
] }));
vi.mock("../../platform/local-review-pages", () => ({
  readReviewRepositories: async (input: PagedReviewInput) => ({ repositories: [input.path] }),
  readReviewManifest: async (input: PagedReviewInput) => reviewManifestFixture(input),
  readReviewFile: async (input: PagedReviewInput) => reviewFileFixture(input, "+" + input.task_id + "-change"),
  readReviewCommits: vi.fn(),
  renewReviewLease: async (input: PagedReviewInput) => ({ version_id: input.version_id || "", expires_at: "2100-01-01T00:00:00Z" }),
}));
afterEach(cleanup);

describe("remote worktree entry points", () => {
  it("agent Work shows only that agent's worktrees and opens the matching task", async () => {
    renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalWorktreeReviews workspaceId="ws" agentId="agent-b" /></QueryClientProvider>, { locale: "zh-Hans" });
    expect(await screen.findByText(/\/remote\/worktree-b/)).toBeInTheDocument();
    expect(screen.queryByText(/\/remote\/worktree-a/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "查看 / 提交 MR" }));
    expect(await screen.findByText("+task-b-change")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveTextContent("/remote/worktree-b");
  });
  it("workspace management shows worktrees on multiple runtimes", async () => {
    renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalWorktreeReviews workspaceId="ws" /></QueryClientProvider>, { locale: "zh-Hans" });
    expect(await screen.findByText(/\/remote\/worktree-a/)).toBeInTheDocument();
    expect(screen.getByText(/\/remote\/worktree-b/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "查看 / 提交 MR" })).toHaveLength(2);
    const [first] = screen.getAllByRole("button", { name: "查看 / 提交 MR" });
    if (!first) throw new Error("MR entry missing");
    fireEvent.click(first);
    expect(await screen.findByText("+task-a-change")).toBeInTheDocument();
  });
});
