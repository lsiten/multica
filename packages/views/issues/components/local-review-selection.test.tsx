// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { reviewManifestFixture } from "../../test/local-review-pages";
import { mergeSelectedFiles, supportsSelectedMerge } from "../../platform/local-review-selection";
import { LocalReviewSelection } from "./local-review-selection";

vi.mock("../../platform/local-review-selection", () => ({ mergeSelectedFiles: vi.fn(), supportsSelectedMerge: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it("requires confirmation and displays conflicts without reporting success", async () => {
  const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main" };
  const manifest = reviewManifestFixture(request);
  vi.mocked(supportsSelectedMerge).mockResolvedValue(true);
  vi.mocked(mergeSelectedFiles).mockResolvedValue({ kind: "selected_merge", version_id: manifest.version_id, target: "main", paths: ["entry.ts"], commit: "", conflicts: ["entry.ts"] });
  const merged = vi.fn();
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewSelection request={request} manifest={manifest} paths={["entry.ts"]} disabled={false} onBusyChange={() => {}} onMerged={merged} /></QueryClientProvider>, { locale: "zh-Hans" });
  fireEvent.change(screen.getByRole("textbox", { name: "选择性合并的提交说明" }), { target: { value: "apply one file" } });
  await waitFor(() => expect(screen.getByRole("button", { name: "合并选中文件" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "合并选中文件" }));
  expect(mergeSelectedFiles).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "确认将 1 个文件合并到 main" }));
  await screen.findByText("entry.ts");
  expect(merged).not.toHaveBeenCalled();
  expect(mergeSelectedFiles).toHaveBeenCalledWith(expect.objectContaining({ paths: ["entry.ts"], version_id: manifest.version_id, message: "apply one file" }));
});
