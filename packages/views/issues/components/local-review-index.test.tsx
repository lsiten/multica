// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { readLocalIndex, changeLocalIndex, renewReviewLease } from "../../platform/local-review-pages";
import { LocalReviewIndex } from "./local-review-index";

vi.mock("../../platform/local-review-pages", () => ({ readLocalIndex: vi.fn(), changeLocalIndex: vi.fn(), renewReviewLease: vi.fn() }));
vi.mock("@multica/core/auth", () => { const state = { user: { id: "viewer" } }; return { useAuthStore: Object.assign((select: (value: typeof state) => unknown) => select(state), { getState: () => state }) }; });
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers(); });
beforeEach(() => { vi.mocked(renewReviewLease).mockResolvedValue({ version_id: "a".repeat(64), expires_at: "2100-01-01T00:00:00Z" }); });

it("renews the independent staging snapshot while mounted and stops afterward", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
  const version = "a".repeat(64);
  vi.mocked(readLocalIndex).mockResolvedValue({ kind: "index", version_id: version, runtime_id: "runtime", status: { branch: "feature", head: "c".repeat(40), index_id: "b".repeat(64), files: [] } });
  vi.mocked(renewReviewLease).mockResolvedValue({ version_id: version, expires_at: "2100-01-01T00:00:00Z" });
  const view = renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewIndex request={{ task_id: "task", workspace_id: "ws", path: "/repo", target: "production" }} onChanged={() => {}} onBusyChange={() => {}} /></QueryClientProvider>, { locale: "zh-Hans" });
  await waitFor(() => expect(renewReviewLease).toHaveBeenCalled());
  const initial = vi.mocked(renewReviewLease).mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
  expect(vi.mocked(renewReviewLease).mock.calls.length).toBeGreaterThan(initial);
  expect(renewReviewLease).toHaveBeenLastCalledWith(expect.objectContaining({ version_id: version, target: "feature", runtime_id: "runtime" }), expect.any(AbortSignal));
  view.unmount();
  const stopped = vi.mocked(renewReviewLease).mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(renewReviewLease).toHaveBeenCalledTimes(stopped);
});
it("stages only the selected file and requires confirmation before commit", async () => {
  const version = "a".repeat(64), index = "b".repeat(64), head = "c".repeat(40);
  vi.mocked(readLocalIndex).mockResolvedValue({ kind: "index", version_id: version, status: { branch: "feature", head, index_id: index, files: [
    { path: "working.ts", index_code: " ", working_code: "M", staged: false, unstaged: true, untracked: false, conflicted: false, unsupported: false },
    { path: "staged.ts", index_code: "M", working_code: " ", staged: true, unstaged: false, untracked: false, conflicted: false, unsupported: false },
  ] } });
  vi.mocked(changeLocalIndex).mockResolvedValue({ kind: "index_result", result: { index_id: "d".repeat(64) } });
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewIndex request={{ task_id: "task", workspace_id: "ws", path: "/repo", target: "main" }} onChanged={() => {}} onBusyChange={() => {}} /></QueryClientProvider>, { locale: "zh-Hans" });
  fireEvent.click(await screen.findByRole("button", { name: "暂存 working.ts" }));
  await waitFor(() => expect(changeLocalIndex).toHaveBeenCalledWith(expect.objectContaining({ action: "stage", paths: ["working.ts"], index_id: index, version_id: version, target: "feature" })));
  await screen.findByRole("button", { name: "创建 Commit" });
  fireEvent.change(screen.getByRole("textbox", { name: "提交说明" }), { target: { value: "selected commit" } });
  await waitFor(() => expect(screen.getByRole("button", { name: "创建 Commit" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "创建 Commit" }));
  expect(changeLocalIndex).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "确认提交 1 个暂存文件" }));
  await waitFor(() => expect(changeLocalIndex).toHaveBeenLastCalledWith(expect.objectContaining({ action: "commit", message: "selected commit", index_id: index })));
});
