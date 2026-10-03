import { beforeEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentRuntime } from "@multica/core/types";
import { parseManagedWorktrees, type WorktreeCacheResult } from "@multica/core/types/managed-worktree";
import { parseEnvironmentOperationStatus, type EnvironmentCommand } from "@multica/core/types/environment-operations";
import { renderWithI18n } from "../../test/i18n";
import { RuntimeEnvironments } from "./runtime-environments-tab";

const mocks = vi.hoisted(() => ({ list: vi.fn(), execute: vi.fn() }));
const policyStatus = { workspace_id: "ws", runtime_id: "runtime", policy: { enabled: true, archive_after_hours: 24, cache_after_hours: 12, pressure_cache_after_hours: 1, max_idle_environments: 100, max_directory_bytes: 20 * 1024 ** 3, minimum_free_bytes: 5 * 1024 ** 3 }, effective_enabled: true, scan_interval_seconds: 300, free_bytes: null, last_scan_at: null, idle_environments: 0, directory_bytes: 0, under_pressure: false };
vi.mock("@multica/core/api", () => ({ api: { listEnvironmentRuntimes: mocks.list, executeRuntimeEnvironment: mocks.execute } }));

const runtime: AgentRuntime = { id: "runtime", workspace_id: "ws", daemon_id: "daemon", name: "Other computer", runtime_mode: "local", provider: "codex", launch_header: "", status: "online", device_info: "", metadata: {}, owner_id: "user", visibility: "private", last_seen_at: null, created_at: "2026-10-03T00:00:00Z", updated_at: "2026-10-03T00:00:00Z" };
const receipt = (status = "running") => parseEnvironmentOperationStatus({ id: "a".repeat(64), action: "archive", status, automatic: true, workspace_id: "ws", runtime_id: "runtime", daemon_id: "daemon", profile: "test", started_at: "2026-10-03T00:00:00Z", updated_at: "2026-10-03T00:00:00Z", completed: 0, total: 1, results: [], error: "" });

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return renderWithI18n(<QueryClientProvider client={client}><RuntimeEnvironments workspaceId="ws" workspaceSlug="workspace" userId="user" /></QueryClientProvider>, { locale: "zh-Hans" });
}

async function selectRuntime() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("combobox", { name: "选择运行时" }));
  await user.click(await screen.findByRole("option", { name: "Other computer" }));
  return user;
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.list.mockResolvedValue([runtime]);
  mocks.execute.mockImplementation(async (_workspace: string, _runtime: string, command: EnvironmentCommand) => {
    if (command.action === "policy") return policyStatus;
    if (command.action === "operations") return [receipt()];
    if (command.action === "operation_status") return receipt();
    if (command.action === "operation_cancel") return receipt("cancelled");
    return [];
  });
});

it("recovers an automatic operation on another runtime and allows cancellation", async () => {
  mount();
  const user = await selectRuntime();
  expect(await screen.findByRole("status")).toHaveTextContent("处理中");
  expect(screen.getByRole("combobox", { name: "回收记录" })).toHaveTextContent("自动回收");
  expect(screen.getByRole("button", { name: "归档闲置工作副本" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "取消操作" }));
  await waitFor(() => expect(mocks.execute).toHaveBeenCalledWith("ws", "runtime", { action: "operation_cancel", operation_id: "a".repeat(64) }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("已取消"));
});

it("keeps offline runtimes visible without inspecting their filesystem", async () => {
  mocks.list.mockResolvedValue([{ ...runtime, status: "offline" }]);
  mount(); await selectRuntime();
  expect(await screen.findByText("运行时已离线，暂时无法查看或操作其本地环境。")).toBeInTheDocument();
  expect(mocks.execute).not.toHaveBeenCalled();
});

it("excludes runtimes owned by other users", async () => {
  mocks.list.mockResolvedValue([runtime, { ...runtime, id: "foreign", name: "Private computer", owner_id: "other-user" }]);
  mount();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("combobox", { name: "选择运行时" }));
  expect(within(await screen.findByRole("listbox")).queryByRole("option", { name: "Private computer" })).not.toBeInTheDocument();
  expect(mocks.list).toHaveBeenCalledWith("ws", "workspace");
});

it("executes all previewed environments in sequential bounded batches", async () => {
  const selections = Array.from({ length: 1001 }, (_, index) => index.toString(16).padStart(64, "0"));
  const rows = parseManagedWorktrees(selections.map((id) => ({ environment_id: id, workspace_id: "ws", task_short: id.slice(-3), kind: "issue", path: `/runtime/${id}`, active: false, size_bytes: 1 })));
  const previews: WorktreeCacheResult[] = selections.map((id) => ({ environmentId: id, revision: "b".repeat(64), workspaceId: "ws", taskId: id, candidates: [{ path: "codex-home/.sandbox-bin", sizeBytes: 1 }], sizeBytes: 1, reason: "", removedBytes: 0, removedCount: 0 }));
  const records = new Map<string, ReturnType<typeof receipt>>();
  mocks.execute.mockImplementation(async (_workspace: string, _runtime: string, command: EnvironmentCommand) => {
    switch (command.action) {
      case "policy": return policyStatus;
      case "inventory": return rows;
      case "cache_preview": return previews;
      case "operations": return [...records.values()];
      case "operation_start": {
        const status = { ...receipt("completed"), id: command.operation.id, automatic: false, total: command.operation.selections.length, completed: command.operation.selections.length };
        records.set(status.id, status);
        return status;
      }
      case "operation_status": return records.get(command.operation_id);
      default: return [];
    }
  });
  mount();
  const user = await selectRuntime();
  await user.click(await screen.findByRole("button", { name: "扫描可回收缓存" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "清理缓存" }));
  await waitFor(() => expect(records.size).toBe(2));
  const started = mocks.execute.mock.calls.map((call) => call[2]).filter((command: EnvironmentCommand) => command.action === "operation_start");
  expect(started.map((command) => command.operation.selections.length)).toEqual([1000, 1]);
  expect(started.flatMap((command) => command.operation.selections.map((selection: { environment_id: string }) => selection.environment_id))).toEqual(selections);
});
