// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { reviewFileFixture, reviewManifestFixture } from "../../test/local-review-pages";
import { LocalReviewDialog } from "./local-review-dialog";
import { readLocalReviewBranches } from "../../platform/local-review";
import { readReviewCommits, readReviewFile, readReviewManifest, readReviewRepositories, renewReviewLease } from "../../platform/local-review-pages";

vi.mock("../../platform/local-review", () => ({ readLocalReviewBranches: vi.fn() }));
vi.mock("@multica/core/auth", () => { const state = { user: { id: "viewer" } }; return { useAuthStore: Object.assign((select: (value: typeof state) => unknown) => select(state), { getState: () => state }) }; });
vi.mock("../../platform/local-review-pages", () => ({ readReviewManifest: vi.fn(), readReviewFile: vi.fn(), readReviewRepositories: vi.fn(), readReviewCommits: vi.fn(), renewReviewLease: vi.fn() }));
beforeEach(() => {
  vi.mocked(readLocalReviewBranches).mockResolvedValue(["main", "release", "test"]);
  vi.mocked(readReviewRepositories).mockResolvedValue({ repositories: ["/repo/feature", "/repo/other"], runtime_id: "discovered" });
  vi.mocked(readReviewManifest).mockImplementation(async (input) => reviewManifestFixture(input));
  vi.mocked(readReviewFile).mockImplementation(async (input) => reviewFileFixture(input));
  vi.mocked(renewReviewLease).mockImplementation(async (input) => ({ version_id: input.version_id || "", expires_at: "2100-01-01T00:00:00Z" }));
});
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers(); });

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(<QueryClientProvider client={client}><LocalReviewDialog request={{ task_id: "task", workspace_id: "ws", path: "/repo/feature", target: "main" }} onClose={() => {}} /></QueryClientProvider>, { locale: "zh-Hans" });
  return client;
}
async function chooseTarget(branch: string) {
  fireEvent.click(screen.getByRole("combobox", { name: "目标分支" }));
  fireEvent.change(await screen.findByRole("combobox", { name: "搜索本地分支…" }), { target: { value: branch } });
  fireEvent.click(await screen.findByRole("option", { name: branch }));
}

describe("paged local MR dialog", () => {
  it("renews the displayed version while open and stops on unmount", async () => {
    vi.useFakeTimers();
    mount();
    await act(async () => { for (let tick = 0; tick < 10; tick++) await vi.advanceTimersByTimeAsync(10); });
    const initial = vi.mocked(renewReviewLease).mock.calls.length;
    expect(initial).toBeGreaterThan(0);
    await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
    expect(vi.mocked(renewReviewLease).mock.calls.length).toBeGreaterThan(initial);
    expect(renewReviewLease).toHaveBeenLastCalledWith(expect.objectContaining({ version_id: "a".repeat(64) }), expect.any(AbortSignal));
    cleanup();
    const stopped = vi.mocked(renewReviewLease).mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
    expect(renewReviewLease).toHaveBeenCalledTimes(stopped);
  });
  it("defaults to an existing priority branch and preserves manual selection", async () => {
    vi.mocked(readLocalReviewBranches).mockResolvedValue(["release", "test"]);
    const client = mount();
    await screen.findByText("+after");
    expect(readReviewManifest).toHaveBeenCalledWith(expect.objectContaining({ target: "test", action: "manifest" }), expect.any(AbortSignal));
    await chooseTarget("release");
    expect(readReviewManifest).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "对比目标分支" }));
    await waitFor(() => expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ target: "release" }), expect.any(AbortSignal)));
    vi.mocked(readLocalReviewBranches).mockResolvedValue(["main", "release", "test"]);
    await client.invalidateQueries({ queryKey: ["local-review-branches"] });
    expect(screen.getByRole("combobox", { name: "目标分支" })).toHaveTextContent("release");
  });
  it("retains discovered runtime identity when choosing targets and repositories", async () => {
    mount();
    await screen.findByText("+after");
    await chooseTarget("release");
    fireEvent.click(screen.getByRole("button", { name: "对比目标分支" }));
    await waitFor(() => expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ target: "release", runtime_id: "discovered" }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByRole("combobox", { name: "仓库" }), { target: { value: "/repo/other" } });
    await waitFor(() => expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ path: "/repo/other", runtime_id: "discovered" }), expect.any(AbortSignal)));
  });
  it("keeps a large file isolated and target selection available", async () => {
    vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: input.version_id || "", path: input.file_path || "", preview: "too_large" }));
    mount();
    await screen.findByText("此文件过大或单行过长，无法以补丁形式预览。其他文件仍可审查。");
    expect(screen.getByRole("combobox", { name: "目标分支" })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("approves the fixed version and merges only after separate confirmation", async () => {
    mount();
    await screen.findByText("+after");
    expect(screen.getByRole("button", { name: "合并" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "确认通过" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "合并" })).toBeEnabled());
    expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ action: "approve", version_id: "a".repeat(64), snapshot_id: "a".repeat(64) }));
    fireEvent.click(screen.getByRole("button", { name: "合并" }));
    expect(readReviewManifest).not.toHaveBeenCalledWith(expect.objectContaining({ action: "merge" }));
    fireEvent.click(screen.getByRole("button", { name: "确认本地合并" }));
    await screen.findByText(/merged-sha/);
    expect(readReviewManifest).toHaveBeenLastCalledWith(expect.objectContaining({ action: "merge", version_id: "a".repeat(64) }));
  });
  it("loads commit history only when expanded and keeps it scoped to the version", async () => {
    vi.mocked(readReviewCommits).mockImplementation(async (input) => ({
      version_id: input.version_id || "", commits: [{ sha: input.offset ? "2".repeat(40) : "1".repeat(40), subject: input.offset ? "older captured commit" : "captured commit" }],
      next_offset: input.offset ? 2 : 1, has_more: !input.offset,
    }));
    mount();
    await screen.findByText("+after");
    expect(readReviewCommits).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("本地提交"));
    await screen.findByText(/captured commit/);
    fireEvent.click(screen.getByRole("button", { name: "加载更多提交" }));
    await screen.findByText(/older captured commit/);
    expect(readReviewCommits).toHaveBeenLastCalledWith(expect.objectContaining({ version_id: "a".repeat(64), offset: 1 }), expect.any(AbortSignal));
  });
  it.each(["merge", "merge_recovered"])("labels %s history as merged", async (kind) => {
    vi.mocked(readReviewManifest).mockImplementation(async (input) => {
      const result = reviewManifestFixture(input);
      result.review.events = [{ kind, snapshot_id: result.version_id, actor_id: "reviewer", actor_name: "审查者", comment: "", created_at: "2026-09-09T00:00:00Z" }];
      return result;
    });
    mount();
    await screen.findByText(/审查者/);
    expect(screen.getByText(/审查者/)).toHaveTextContent("已合并");
    expect(screen.queryByText(/已请求合并/)).not.toBeInTheDocument();
  });
});
