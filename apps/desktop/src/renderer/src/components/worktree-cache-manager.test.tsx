import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { parseManagedWorktrees, type WorktreeCacheResult } from "@multica/core/types/managed-worktree";
import { beforeEach, expect, it, vi } from "vitest";
import { WorktreeCacheManager } from "./worktree-cache-manager";

const previewWorktreeCaches = vi.fn();
const cleanWorktreeCaches = vi.fn();
const receipt = (reason = ""): WorktreeCacheResult => ({ environmentId: "env", workspaceId: "ws", taskId: "task", revision: "revision", candidates: [{ path: "codex-home/.sandbox-bin", sizeBytes: 1024 }], sizeBytes: 1024, removedBytes: 0, removedCount: 0, reason });

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "daemonAPI", { configurable: true, value: { previewWorktreeCaches, cleanWorktreeCaches } });
});

function mount() {
  const rows = parseManagedWorktrees([{ workspace_id: "ws", task_short: "test run", path: "/root", kind: "chat", size_bytes: 2000, environment_id: "env", protection_reason: "output", active: false }]);
  render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="zh-Hans" resources={RESOURCES}><WorktreeCacheManager rows={rows} disabled={false} /></I18nProvider></QueryClientProvider>);
}

it("offers cache cleanup for retained outputs and requires a reviewed preview", async () => {
  previewWorktreeCaches.mockResolvedValue([receipt()]);
  cleanWorktreeCaches.mockResolvedValue([{ ...receipt(), removedCount: 1, removedBytes: 1024 }]);
  mount();
  fireEvent.click(screen.getByRole("button", { name: "扫描可回收缓存" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("保留代码修改、提交、产物、日志和会话");
  expect(dialog).toHaveTextContent("codex-home/.sandbox-bin");
  expect(cleanWorktreeCaches).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole("button", { name: "清理缓存" }));
  await waitFor(() => expect(cleanWorktreeCaches).toHaveBeenCalledWith([{ environmentId: "env", revision: "revision" }], undefined));
  expect(await screen.findByRole("status")).toHaveTextContent("已移除 1 个缓存目录");
});

it("keeps protected environments out of the deletion request", async () => {
  previewWorktreeCaches.mockResolvedValue([receipt("active")]);
  mount();
  fireEvent.click(screen.getByRole("button", { name: "扫描可回收缓存" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("运行中 · 已保护");
  expect(within(dialog).getByRole("button", { name: "清理缓存" })).toBeDisabled();
  expect(cleanWorktreeCaches).not.toHaveBeenCalled();
});

it("shows changed-preview skips without claiming that cache was removed", async () => {
  previewWorktreeCaches.mockResolvedValue([receipt()]);
  cleanWorktreeCaches.mockResolvedValue([receipt("preview_changed")]);
  mount();
  fireEvent.click(screen.getByRole("button", { name: "扫描可回收缓存" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "清理缓存" }));
  const result = await screen.findByRole("status");
  expect(result).toHaveTextContent("缓存已变化，请重新扫描");
  expect(result).toHaveTextContent("已移除 0 个缓存目录");
});
