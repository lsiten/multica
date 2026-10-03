import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";
import { emptyWorktreeFilters } from "@multica/core/types/worktree-filters";
import { beforeEach, expect, it, vi } from "vitest";
import { WorktreeArchiveManager } from "./worktree-archive-manager";

const environmentArchiveOperation = vi.fn();
const listEnvironmentArchives = vi.fn();
const preview = { environmentId: "env", workspaceId: "ws", taskId: "task", revision: "a".repeat(64), archiveId: "", reason: "", originalBytes: 100, archiveBytes: 0, reclaimed: false, restored: false };
const summary = { archiveId: "b".repeat(64), workspaceId: "ws", taskId: "task", taskName: "old run", agentId: "agent", agentName: "Agent", runtimeId: "runtime", projectId: "", projectName: "", squadId: "", squadName: "", kind: "chat", originalPath: "/original", createdAt: "2026-10-03T00:00:00Z", archiveBytes: 10, logicalBytes: 100, restoreReason: "" };

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "daemonAPI", { configurable: true, value: { environmentArchiveOperation, listEnvironmentArchives } });
});

function mount() {
  const rows = parseManagedWorktrees([{ workspace_id: "ws", task_short: "test run", path: "/root", kind: "chat", size_bytes: 100, environment_id: "env", protection_reason: "output", active: false }]);
  render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="zh-Hans" resources={RESOURCES}><WorktreeArchiveManager rows={rows} enabled disabled={false} filters={emptyWorktreeFilters} queryKey={["test-archives", "account", "profile"]} onPendingChange={() => {}} /></I18nProvider></QueryClientProvider>);
}

it("archives retained outputs only after preview confirmation and reports reclamation", async () => {
  environmentArchiveOperation.mockResolvedValueOnce([preview]).mockResolvedValueOnce([{ ...preview, archiveId: "b".repeat(64), reclaimed: true, archiveBytes: 10 }]);
  mount();
  fireEvent.click(screen.getByRole("button", { name: "归档闲置工作副本" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("归档校验成功后才移除工作副本");
  expect(environmentArchiveOperation).toHaveBeenCalledTimes(1);
  fireEvent.click(within(dialog).getByRole("button", { name: "归档并回收" }));
  await waitFor(() => expect(environmentArchiveOperation).toHaveBeenLastCalledWith(expect.objectContaining({ action: "archive", operationId: expect.stringMatching(/^[a-f0-9]{64}$/), selections: [{ environmentId: "env", revision: "a".repeat(64) }] })));
  expect(await screen.findByRole("status")).toHaveTextContent("已归档并回收");
});

it("loads collapsed archive history on demand and restores by archive identity", async () => {
  listEnvironmentArchives.mockResolvedValue([summary]);
  environmentArchiveOperation.mockResolvedValue([{ ...preview, archiveId: summary.archiveId, restored: true }]);
  mount();
  expect(listEnvironmentArchives).not.toHaveBeenCalled();
  const details = screen.getByText("已归档环境").closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  fireEvent.click(await screen.findByRole("button", { name: "恢复工作副本" }));
  const dialog = screen.getByRole("dialog");
  expect(dialog).toHaveTextContent("不覆盖已有目录");
  expect(dialog).toHaveTextContent("/original");
  fireEvent.click(within(dialog).getByRole("button", { name: "恢复工作副本" }));
  await waitFor(() => expect(environmentArchiveOperation).toHaveBeenCalledWith({ action: "restore", workspaceId: "ws", archiveId: summary.archiveId }));
  expect(await screen.findByRole("status")).toHaveTextContent("已恢复工作副本");
});

it("preserves failed reclamation receipts and does not label them as success", async () => {
  environmentArchiveOperation.mockResolvedValueOnce([preview]).mockResolvedValueOnce([{ ...preview, archiveId: "b".repeat(64), reason: "reclaim_failed", archiveBytes: 10 }]);
  mount();
  fireEvent.click(screen.getByRole("button", { name: "归档闲置工作副本" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "归档并回收" }));
  expect(await screen.findByRole("status")).toHaveTextContent("归档已保存，目录回收未完成");
  expect(screen.getByRole("status")).not.toHaveTextContent("已归档并回收");
});
