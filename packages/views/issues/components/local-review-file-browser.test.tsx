// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { onlineManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { renderWithI18n } from "../../test/i18n";
import { readReviewContent, readReviewContext, readReviewFile, readReviewManifest } from "../../platform/local-review-pages";
import { LocalReviewFileBrowser } from "./local-review-file-browser";

vi.mock("../../platform/local-review-pages", () => ({ readReviewFile: vi.fn(), readReviewManifest: vi.fn(), readReviewContent: vi.fn(), readReviewContext: vi.fn() }));
afterEach(() => { cleanup(); onlineManager.setOnline(true); vi.clearAllMocks(); });
const id = "a".repeat(64);
const manifest: ReviewManifest = {
  version_id: id, header: { repository: "/repo", branch: "feature", target: "main", head: "head", target_head: "target", base: "base", dirty: false, committed: false },
  page: { files: [{ path: "a.ts", status: "modified", preview: "text", additions: 2, deletions: 1 }], total_files: 2, next_offset: 1, has_more: true, additions: 4, deletions: 2 },
  review: { snapshot_id: id, state: "open", comment: "", merged_commit: "", events: [] },
};
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main" };

it("expands omitted original lines inline and can collapse them again", async () => {
  vi.mocked(readReviewFile).mockResolvedValue({ version_id: id, path: "a.ts", preview: "text", page: { lines: [{ text: "@@ -5 +5 @@", kind: "meta", context_old_start: 1, context_new_start: 1, context_lines: 4 }], next_line: 1, has_more: false } });
  vi.mocked(readReviewContext).mockResolvedValue({ version_id: id, path: "a.ts", preview: "text", page: { lines: [{ text: "原始上下文", kind: "context", new_line: 1 }], next_line: 1, has_more: true } });
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  fireEvent.click(await screen.findByRole("button", { name: "展开 4 行上下文" }));
  await screen.findByText("原始上下文");
  expect(readReviewContext).toHaveBeenCalledWith(expect.objectContaining({ version_id: id, file_path: "a.ts", offset: 0, limit: 4 }), expect.any(AbortSignal));
  fireEvent.click(screen.getByRole("button", { name: "收起上下文" }));
  expect(screen.queryByText("原始上下文")).not.toBeInTheDocument();
});

it("reads local patches even when the browser reports offline", async () => {
  onlineManager.setOnline(false);
  vi.mocked(readReviewFile).mockResolvedValue({ version_id: id, path: "a.ts", preview: "text", page: { lines: [{ text: "+local-offline", kind: "add", new_line: 1 }], next_line: 1, has_more: false } });
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("+local-offline");
});

it("moves across file boundaries when no patch page remains", async () => {
  const multiple: ReviewManifest = { ...manifest, page: { ...manifest.page, has_more: false, files: [
    ...manifest.page.files, { path: "b.ts", status: "added", preview: "text", additions: 1, deletions: 0 },
  ] } };
  vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: id, path: input.file_path || "", preview: "text", page: { lines: [{ text: "+" + input.file_path, kind: "add", new_line: 1 }], next_line: 1, has_more: false } }));
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={multiple} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("+a.ts");
  fireEvent.click(screen.getByRole("button", { name: "下一段" }));
  await screen.findByText("+b.ts");
  fireEvent.click(screen.getByRole("button", { name: "上一段" }));
  await screen.findByText("+a.ts");
});

it("loads the next file-list page when advancing past the loaded files", async () => {
  vi.mocked(readReviewManifest).mockResolvedValue({ ...manifest, page: { ...manifest.page, files: [{ path: "b.ts", status: "added", preview: "text", additions: 1, deletions: 0 }], has_more: false, next_offset: 2 } });
  vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: id, path: input.file_path || "", preview: "text", page: { lines: [{ text: "+" + input.file_path, kind: "add", new_line: 1 }], next_line: 1, has_more: false } }));
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("+a.ts");
  fireEvent.click(screen.getByRole("button", { name: "下一段" }));
  await screen.findByText("+b.ts");
  expect(readReviewManifest).toHaveBeenCalledWith(expect.objectContaining({ action: "files", offset: 1 }), expect.any(AbortSignal));
});

it("cancels an unfinished patch read when its viewer unmounts", async () => {
  let readerSignal: AbortSignal | undefined;
  let finish = () => {};
  vi.mocked(readReviewFile).mockImplementation((_input, signal) => {
    readerSignal = signal;
    return new Promise((resolve) => { finish = () => resolve({ version_id: id, path: "a.ts", preview: "text", page: { lines: [], next_line: 0, has_more: false } }); });
  });
  const view = renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  try {
    await waitFor(() => expect(readReviewFile).toHaveBeenCalledTimes(1));
    expect(readerSignal?.aborted).toBe(false);
    view.unmount();
    expect(readerSignal?.aborted).toBe(true);
  } finally {
    view.unmount();
    finish();
  }
});

it("cancels the previous file read while leaving the newly selected file active", async () => {
  const signals: (AbortSignal | undefined)[] = [];
  const finish: (() => void)[] = [];
  vi.mocked(readReviewFile).mockImplementation((input, signal) => {
    signals.push(signal);
    return new Promise((resolve) => { finish.push(() => resolve({ version_id: id, path: input.file_path || "", preview: "binary" })); });
  });
  const twoFiles: ReviewManifest = { ...manifest, page: { ...manifest.page, has_more: false, files: [
    ...manifest.page.files, { path: "b.ts", status: "added", preview: "text", additions: 1, deletions: 0 },
  ] } };
  const view = renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={twoFiles} /></QueryClientProvider>, { locale: "zh-Hans" });
  try {
    await waitFor(() => expect(signals).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: /b.ts/ }));
    await waitFor(() => expect(signals).toHaveLength(2));
    expect(signals[0]?.aborted).toBe(true);
    expect(signals[1]?.aborted).toBe(false);
  } finally {
    view.unmount();
    for (const resolve of finish) resolve();
  }
});

it("loads file metadata and selected patches separately, and keeps only one patch page rendered", async () => {
  vi.mocked(readReviewManifest).mockResolvedValue({ ...manifest, page: { ...manifest.page, files: [{ path: "b.ts", status: "added", preview: "text", additions: 2, deletions: 0 }], next_offset: 2, has_more: false } });
  vi.mocked(readReviewFile).mockImplementation(async (input) => ({ version_id: id, path: input.file_path ?? "", preview: "text", page: { lines: [{ text: input.offset ? "+second page" : "+first page", kind: "add", new_line: input.offset ? 2 : 1 }], next_line: input.offset ? 2 : 1, has_more: !input.offset } }));
  renderWithI18n(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("+first page");
  const initialScroller = screen.getByText("+first page").closest("pre")?.parentElement;
  if (!initialScroller) throw new Error("Missing patch scroll container");
  initialScroller.scrollTop = 400;
  expect(readReviewFile).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "加载更多文件" }));
  await screen.findByRole("button", { name: /b.ts/ });
  expect(readReviewFile).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "下一段" }));
  await screen.findByText("+second page");
  expect(screen.getByText("+second page").closest("pre")?.parentElement?.scrollTop).toBe(0);
  expect(screen.queryByText("+first page")).not.toBeInTheDocument();
  expect(readReviewFile).toHaveBeenLastCalledWith(expect.objectContaining({ version_id: id, file_path: "a.ts", offset: 1 }), expect.any(AbortSignal));
  fireEvent.click(screen.getByRole("button", { name: "上一段" }));
  await screen.findByText("+first page");
  expect(screen.queryByText("+second page")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /b.ts/ }));
  await waitFor(() => expect(readReviewFile).toHaveBeenLastCalledWith(expect.objectContaining({ file_path: "b.ts", offset: 0 }), expect.any(AbortSignal)));
});

it("keeps other files accessible when the selected file cannot be previewed", async () => {
  const mixed: ReviewManifest = { ...manifest, page: { ...manifest.page, files: [
    { path: "binary.bin", status: "added", preview: "binary", additions: 0, deletions: 0 },
    { path: "source.ts", status: "modified", preview: "text", additions: 1, deletions: 0 },
  ], next_offset: 2, has_more: false } };
  vi.mocked(readReviewFile).mockImplementation(async (input) => input.file_path === "binary.bin"
    ? { version_id: id, path: "binary.bin", preview: "binary" }
    : { version_id: id, path: "source.ts", preview: "text", page: { lines: [{ text: "+reviewable", kind: "add", new_line: 1 }], next_line: 1, has_more: false } });
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={mixed} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("二进制文件，不显示文本差异。");
  fireEvent.click(screen.getByRole("button", { name: /source.ts/ }));
  await screen.findByText("+reviewable");
  expect(screen.queryByText("二进制文件，不显示文本差异。")).not.toBeInTheDocument();
});

it("offers fixed content pages and before/after switching when a patch cannot be shown", async () => {
  vi.mocked(readReviewFile).mockResolvedValue({ version_id: id, path: "a.ts", preview: "too_large" });
  vi.mocked(readReviewContent).mockImplementation(async (input) => ({
    version_id: id, path: input.file_path || "", side: input.side || "new",
    content: { text: input.side === "old" ? "旧内容" : input.offset ? "下一段" : "固定内容", encoding: "utf8", offset: input.offset || 0, next_offset: input.offset ? 24 : 12, size: 24, has_more: !input.offset },
  }));
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewFileBrowser request={request} manifest={manifest} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("此文件过大或单行过长，无法以补丁形式预览。其他文件仍可审查。");
  fireEvent.click(screen.getByRole("button", { name: "查看文件内容" }));
  await screen.findByText("固定内容");
  fireEvent.click(screen.getByRole("button", { name: "下一段" }));
  await screen.findByText("下一段", { selector: "pre" });
  expect(screen.queryByText("固定内容")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "修改前" }));
  await screen.findByText("旧内容");
  expect(readReviewContent).toHaveBeenLastCalledWith(expect.objectContaining({ version_id: id, side: "old", offset: 0 }), expect.any(AbortSignal));
});
