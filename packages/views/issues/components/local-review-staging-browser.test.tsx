// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { reviewManifestFixture } from "../../test/local-review-pages";
import { changeLocalIndex, readLocalIndex, readReviewManifest, readReviewFile, renewReviewLease } from "../../platform/local-review-pages";
import { LocalReviewStagingBrowser } from "./local-review-staging-browser";

vi.mock("@multica/core/auth", () => { const state = { user: { id: "viewer" } }; return { useAuthStore: Object.assign((select: (value: typeof state) => unknown) => select(state), { getState: () => state }) }; });
vi.mock("../../platform/local-review-pages", () => ({ readLocalIndex: vi.fn(), readReviewManifest: vi.fn(), readReviewFile: vi.fn(), renewReviewLease: vi.fn(), changeLocalIndex: vi.fn() }));
vi.mock("../../platform/local-review-selection", () => ({ supportsSelectedMerge: vi.fn(async () => true), mergeSelectedFiles: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers(); });

it("adds hidden file pages and removes all pending files without staging Git changes", async () => {
  const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main" };
  const manifest = reviewManifestFixture(request);
  manifest.page.total_files = 2;
  manifest.page.has_more = true;
  const later = reviewManifestFixture(request, "later.ts");
  later.page.total_files = 2;
  later.page.next_offset = 2;
  vi.mocked(readLocalIndex).mockResolvedValue({ kind: "index", version_id: manifest.version_id, status: { branch: "feature", head: "c".repeat(40), index_id: "e".repeat(64), files: [] } });
  vi.mocked(readReviewManifest).mockResolvedValue(later);
  vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: input.version_id || manifest.version_id, path: input.file_path || "", preview: "binary" }));
  vi.mocked(renewReviewLease).mockImplementation(async (input) => ({ version_id: input.version_id || "", expires_at: "2100-01-01T00:00:00Z" }));
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewStagingBrowser request={request} manifest={manifest} disabled={false} onBusyChange={() => {}} onChanged={() => {}} /></QueryClientProvider>, { locale: "zh-Hans" });
  await waitFor(() => expect(readLocalIndex).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "全部添加" }));
  await waitFor(() => expect(screen.getByText(/待合并文件 · 2 → main/)).toBeTruthy());
  expect(screen.getByText("later.ts")).toBeTruthy();
  expect(screen.queryByText("暂存与 Commit")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "全部移除" }));
  await waitFor(() => expect(screen.queryByText(/待合并文件 · 2 → main/)).toBeNull());
  expect(changeLocalIndex).not.toHaveBeenCalled();
});

it("protects working and staged snapshots without opening the Commit panel", async () => {
  vi.useFakeTimers();
  const working = "b".repeat(64), staged = "d".repeat(64);
  const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "production" };
  vi.mocked(readLocalIndex).mockResolvedValue({ kind: "index", version_id: working, staged_version_id: staged, status: { branch: "feature", head: "c".repeat(40), index_id: "e".repeat(64), files: [] } });
  vi.mocked(readReviewManifest).mockImplementation(async (input) => reviewManifestFixture(input));
  vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: input.version_id || working, path: input.file_path || "", preview: "binary" }));
  vi.mocked(renewReviewLease).mockImplementation(async (input) => ({ version_id: input.version_id || "", expires_at: "2100-01-01T00:00:00Z" }));
  const view = renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewStagingBrowser request={request} manifest={reviewManifestFixture(request)} disabled={false} onBusyChange={() => {}} onChanged={() => {}} /></QueryClientProvider>, { locale: "zh-Hans" });
  await act(async () => { for (let tick = 0; tick < 10; tick++) await vi.advanceTimersByTimeAsync(10); });
  expect(renewReviewLease).toHaveBeenCalledWith(expect.objectContaining({ version_id: staged, target: "feature" }), expect.any(AbortSignal));
  expect(renewReviewLease).toHaveBeenCalledWith(expect.objectContaining({ version_id: working, target: "feature" }), expect.any(AbortSignal));
  const initial = vi.mocked(renewReviewLease).mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
  expect(vi.mocked(renewReviewLease).mock.calls.length).toBeGreaterThan(initial);
  view.unmount();
  const stopped = vi.mocked(renewReviewLease).mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(renewReviewLease).toHaveBeenCalledTimes(stopped);
});
