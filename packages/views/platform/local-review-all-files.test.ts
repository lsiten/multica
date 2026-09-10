// @vitest-environment node
import { expect, it, vi } from "vitest";
import { readAllReviewPaths } from "./local-review-all-files";
import { readReviewManifest } from "./local-review-pages";
import { reviewManifestFixture } from "../test/local-review-pages";

vi.mock("./local-review-pages", () => ({ readReviewManifest: vi.fn() }));
it("collects every immutable file page rather than only visible rows", async () => {
  const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main" };
  const first = reviewManifestFixture(request);
  first.page.total_files = 2; first.page.has_more = true; first.page.next_offset = 1;
  const second = { ...first, page: { ...first.page, files: reviewManifestFixture(request, "later.ts").page.files, has_more: false, next_offset: 2 } };
  vi.mocked(readReviewManifest).mockResolvedValue(second);
  const signal = new AbortController().signal;
  expect(await readAllReviewPaths({ request, manifest: first }, signal)).toEqual(["entry.ts", "later.ts"]);
  expect(readReviewManifest).toHaveBeenCalledWith(expect.objectContaining({ action: "files", version_id: first.version_id, offset: 1 }), signal);
});
it("does not return a partial selection when a later page fails", async () => {
  const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main" };
  const first = reviewManifestFixture(request);
  first.page.total_files = 2; first.page.has_more = true; first.page.next_offset = 1;
  vi.mocked(readReviewManifest).mockRejectedValue(new Error("unavailable"));
  await expect(readAllReviewPaths({ request, manifest: first })).rejects.toThrow("unavailable");
});
