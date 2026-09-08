// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { LocalReviewDialog } from "./local-review-dialog";
import { readLocalReview } from "../../platform/local-review";
import type { LocalReviewSnapshot } from "@multica/core/types/local-review";

vi.mock("../../platform/local-review", () => ({ readLocalReview: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

const snapshot: LocalReviewSnapshot = {
  repositories: ["/repo/feature"],
  id: "snapshot-1", path: "/repo/feature", branch: "feature", target: "main", head: "source-sha", target_head: "target-sha", base: "base-sha", dirty: false, branches: ["main", "feature"], commits: "source-sha Fix bug",
  files: [{ path: "app.ts", status: "tracked", patch: "@@ -1 +1 @@\n-before\n+after" }],
  review: { snapshot_id: "snapshot-1", state: "open", comment: "", merged_commit: "" },
};

describe("local MR dialog", () => {
  it("retains discovered runtime identity when changing the comparison target", async () => {
    vi.mocked(readLocalReview).mockResolvedValue({ ...snapshot, runtime_id: "discovered-runtime" });
    renderWithI18n(<QueryClientProvider client={new QueryClient()}>
      <LocalReviewDialog request={{ task_id: "legacy-task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    await screen.findByText("+after");
    fireEvent.change(screen.getByRole("combobox", { name: "目标分支" }), { target: { value: "release" } });
    fireEvent.click(screen.getByRole("button", { name: "对比目标分支" }));
    await waitFor(() => expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ target: "release", runtime_id: "discovered-runtime" })));
  });
  it("retains discovered runtime identity when selecting another repository", async () => {
    vi.mocked(readLocalReview).mockResolvedValue({ ...snapshot, runtime_id: "discovered-runtime", repositories: ["/repo/feature", "/repo/other"] });
    renderWithI18n(<QueryClientProvider client={new QueryClient()}>
      <LocalReviewDialog request={{ task_id: "legacy-task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    await screen.findByText("+after");
    fireEvent.change(screen.getByRole("combobox", { name: "仓库" }), { target: { value: "/repo/other" } });
    await waitFor(() => expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ path: "/repo/other", runtime_id: "discovered-runtime" })));
  });
  it.each(["merge", "merge_recovered"])("shows %s as completed rather than submitted", async (kind) => {
    vi.mocked(readLocalReview).mockResolvedValue({ ...snapshot, history: [{ kind, snapshot_id: snapshot.id, comment: "", actor_id: "member-id", actor_name: "Reviewer", created_at: "2026-09-08T08:00:00Z" }] });
    renderWithI18n(<QueryClientProvider client={new QueryClient()}>
      <LocalReviewDialog request={{ task_id: "task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    await screen.findByText("审查记录");
    fireEvent.click(screen.getByText("审查记录"));
    expect(screen.getByText(/Reviewer · 已合并/)).toBeInTheDocument();
  });
  it("shows persisted review actors and comments", async () => {
    vi.mocked(readLocalReview).mockResolvedValue({ ...snapshot, history: [{ kind: "request_changes", snapshot_id: snapshot.id, comment: "Please cover the retry path", actor_id: "member-id", actor_name: "Reviewer", created_at: "2026-09-08T08:00:00Z" }] });
    renderWithI18n(<QueryClientProvider client={new QueryClient()}>
      <LocalReviewDialog request={{ task_id: "task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    await screen.findByText("审查记录");
    fireEvent.click(screen.getByText("审查记录"));
    expect(screen.getByText(/Reviewer/)).toBeInTheDocument();
    expect(screen.getByText("Please cover the retry path")).toBeInTheDocument();
  });
  it("does not create or dispatch reviews for a partially typed target", async () => {
    vi.mocked(readLocalReview).mockResolvedValue(snapshot);
    renderWithI18n(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <LocalReviewDialog request={{ task_id: "task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    await screen.findByText("+after");
    const calls = vi.mocked(readLocalReview).mock.calls.length;
    fireEvent.change(screen.getByRole("combobox", { name: "目标分支" }), { target: { value: "release" } });
    expect(readLocalReview).toHaveBeenCalledTimes(calls);
    expect(screen.getByRole("button", { name: "确认通过" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "对比目标分支" }));
    await waitFor(() => expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ target: "release" })));
  });
  it("shows real diff, approves its snapshot and requires a separate merge confirmation", async () => {
    vi.mocked(readLocalReview).mockImplementation(async (request) => ({ ...snapshot, review: { ...snapshot.review, state: request.action === "approve" ? "approved" : request.action === "merge" ? "merged" : "open", merged_commit: request.action === "merge" ? "merged-sha" : "" } }));
    renderWithI18n(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <LocalReviewDialog request={{ task_id: "task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} />
    </QueryClientProvider>, { locale: "zh-Hans" });
    expect(await screen.findByText("+after")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "合并" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "确认通过" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "合并" })).toBeEnabled());
    expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ action: "approve", snapshot_id: "snapshot-1" }));
    fireEvent.click(screen.getByRole("button", { name: "合并" }));
    expect(readLocalReview).not.toHaveBeenCalledWith(expect.objectContaining({ action: "merge" }));
    fireEvent.click(screen.getByRole("button", { name: "确认本地合并" }));
    expect(await screen.findByText(/merged-sha/)).toBeInTheDocument();
    expect(readLocalReview).toHaveBeenLastCalledWith(expect.objectContaining({ action: "merge", snapshot_id: "snapshot-1", target: "main" }));
  });
});
