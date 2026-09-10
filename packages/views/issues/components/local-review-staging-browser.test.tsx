// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { reviewManifestFixture } from "../../test/local-review-pages";
import { readLocalIndex, readReviewManifest, readReviewFile, renewReviewLease } from "../../platform/local-review-pages";
import { LocalReviewStagingBrowser } from "./local-review-staging-browser";

vi.mock("@multica/core/auth", () => { const state = { user: { id: "viewer" } }; return { useAuthStore: Object.assign((select: (value: typeof state) => unknown) => select(state), { getState: () => state }) }; });
vi.mock("../../platform/local-review-pages", () => ({ readLocalIndex: vi.fn(), readReviewManifest: vi.fn(), readReviewFile: vi.fn(), renewReviewLease: vi.fn(), changeLocalIndex: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers(); });

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
