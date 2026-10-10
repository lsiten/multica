import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { WorkerProcessManager } from "./worker-processes";
import type { DaemonWorkerProcess } from "../../../shared/daemon-types";
import { toast } from "sonner";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }));

const getWorkerProcesses = vi.fn();
const stopWorkerProcess = vi.fn();

const worker = (over: Partial<DaemonWorkerProcess> = {}): DaemonWorkerProcess => ({
  exec_id: "exec-1",
  instance_id: "instance-1",
  task_id: "task-1",
  provider: "claude",
  state: "ready",
  ready: true,
  started_at: "2026-10-11T00:00:00Z",
  ...over,
});

function mount(status: { state: string; profile?: string; daemonId?: string }) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <I18nProvider locale="zh-Hans" resources={RESOURCES}>
        <WorkerProcessManager status={status} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "daemonAPI", {
    configurable: true,
    value: { getWorkerProcesses, stopWorkerProcess },
  });
});

describe("worker process panel", () => {
  it("lists live workers with provider, state, task and exec id", async () => {
    getWorkerProcesses.mockResolvedValue([worker({ provider: "codex", task_id: "task-42" })]);
    mount({ state: "running" });
    expect(await screen.findByText("codex")).toBeInTheDocument();
    expect(screen.getByText("ready")).toBeInTheDocument();
    expect(screen.getByText(/task-42/)).toBeInTheDocument();
    expect(screen.getByText(/exec-1/)).toBeInTheDocument();
  });

  it("renders capability chips", async () => {
    getWorkerProcesses.mockResolvedValue([worker({ capabilities: ["task", "gateway"] })]);
    mount({ state: "running" });
    expect(await screen.findByText("gateway")).toBeInTheDocument();
  });

  it("shows the offline notice and never calls the daemon", () => {
    mount({ state: "stopped" });
    expect(screen.getByText("守护进程未运行。")).toBeInTheDocument();
    expect(getWorkerProcesses).not.toHaveBeenCalled();
  });

  it("shows the empty state when no worker is active", async () => {
    getWorkerProcesses.mockResolvedValue([]);
    mount({ state: "running" });
    expect(await screen.findByText("没有活动的工作进程。")).toBeInTheDocument();
  });

  it("surfaces load failures", async () => {
    getWorkerProcesses.mockRejectedValue(new Error("boom"));
    mount({ state: "running" });
    expect(await screen.findByRole("alert")).toHaveTextContent("加载工作进程失败");
  });

  it("cancels the stop confirmation without stopping", async () => {
    getWorkerProcesses.mockResolvedValue([worker()]);
    mount({ state: "running" });
    fireEvent.click(await screen.findByRole("button", { name: "停止" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(stopWorkerProcess).not.toHaveBeenCalled();
  });

  it("stops the worker on confirmation and reports success", async () => {
    getWorkerProcesses.mockResolvedValue([worker()]);
    stopWorkerProcess.mockResolvedValue({ ok: true });
    mount({ state: "running" });
    fireEvent.click(await screen.findByRole("button", { name: "停止" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "停止" }));
    await waitFor(() => expect(stopWorkerProcess).toHaveBeenCalledWith("exec-1"));
    expect(toast.success).toHaveBeenCalled();
  });
});
