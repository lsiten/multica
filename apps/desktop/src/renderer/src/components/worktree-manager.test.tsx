import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { WorktreeManager } from "./worktree-manager";
import type { ManagedWorktree } from "../../../shared/daemon-types";
import { api } from "@multica/core/api";

vi.mock("@multica/core/api", () => ({ api: { listAgents: vi.fn(), listRuntimes: vi.fn() } }));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user" } }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }));

const listWorktrees = vi.fn();
const cleanupWorktrees = vi.fn();
const row = (agentId: string, path: string): ManagedWorktree => ({
  agentId, agentName: "Same name", path, workspaceId: "ws", taskName: path,
  kind: "issue", sizeBytes: 123, active: false, protectionReason: "",
});

function mount() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <I18nProvider locale="zh-Hans" resources={RESOURCES}>
        <WorktreeManager status={{ state: "running", profile: "test", daemonId: "daemon" }} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listAgents).mockResolvedValue([]);
  vi.mocked(api.listRuntimes).mockResolvedValue([]);
  Object.defineProperty(window, "daemonAPI", { configurable: true, value: { listWorktrees, cleanupWorktrees } });
});

describe("worktree management", () => {
  it("groups local worktrees by business agent even when agents share a runtime", async () => {
    listWorktrees.mockResolvedValue([
      { ...row("agent-id", "/first"), agentName: "" },
      { ...row("second-agent", "/third"), agentName: "" },
      { ...row("agent-id", "/second"), agentName: "", workspaceId: "other" },
    ]);
    vi.mocked(api.listAgents).mockImplementation(async (params) => [
      { id: "agent-id", runtime_id: "runtime", name: params?.workspace_id === "ws" ? "开发智能体" : "测试智能体" },
      { id: "second-agent", runtime_id: "runtime", name: "另一个业务智能体" },
      { id: "no-local-worktree", runtime_id: "runtime", name: "没有本地目录的智能体" },
    ] as Awaited<ReturnType<typeof api.listAgents>>);
    vi.mocked(api.listRuntimes).mockImplementation(async (params) => [
      { id: "runtime", name: "codex", custom_name: "本机 Codex", daemon_id: params?.workspace_id === "ws" ? "daemon" : "other-device" },
    ] as Awaited<ReturnType<typeof api.listRuntimes>>);
    mount();
    expect(await screen.findByText("开发智能体 · 1")).toBeInTheDocument();
    expect(await screen.findByText("另一个业务智能体 · 1")).toBeInTheDocument();
    expect(await screen.findByText("测试智能体 · 1")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "清理此智能体" })).toHaveLength(3);
    expect(screen.queryByText("本机 Codex · 2")).not.toBeInTheDocument();
    expect(screen.queryByText(/没有本地目录的智能体/)).not.toBeInTheDocument();
    expect(api.listAgents).toHaveBeenCalledWith({ workspace_id: "ws", include_archived: true });
    expect(api.listAgents).toHaveBeenCalledWith({ workspace_id: "other", include_archived: true });
  });

  it("groups by agent identity, previews exact paths, and waits for cleanup", async () => {
    const first = row("a", "/first");
    const second = row("b", "/second");
    listWorktrees.mockResolvedValue([first, second, { ...row("a", "/busy"), active: true }]);
    let resolveCleanup: (result: { removedPaths: string[]; retained: Record<string, string> }) => void = () => {};
    cleanupWorktrees.mockImplementation(() => new Promise((resolve) => { resolveCleanup = resolve; }));
    mount();
    const buttons = await screen.findAllByRole("button", { name: "清理此智能体" });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]!);
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("/first")).toBeInTheDocument();
    expect(within(dialog).queryByText("/busy")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("/second")).not.toBeInTheDocument();
    expect(cleanupWorktrees).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "清理" }));
    await waitFor(() => expect(cleanupWorktrees).toHaveBeenCalledWith(["/first"], false));
    expect(within(dialog).getByRole("button", { name: "取消" })).toBeDisabled();
    listWorktrees.mockResolvedValue([second]);
    await act(async () => resolveCleanup({ removedPaths: ["/first"], retained: {} }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("requires an explicit discard choice and shows inventory failures", async () => {
    listWorktrees.mockResolvedValue([{ ...row("a", "/dirty"), protectionReason: "dirty" }]);
    cleanupWorktrees.mockResolvedValue({ removedPaths: ["/dirty"], retained: {} });
    mount();
    await waitFor(() => expect(screen.getByRole("button", { name: "清理全部" })).toBeEnabled());
    fireEvent.click(await screen.findByRole("button", { name: "清理全部" }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "清理" }));
    await waitFor(() => expect(cleanupWorktrees).toHaveBeenCalledWith(["/dirty"], true));
    listWorktrees.mockRejectedValue(new Error("Daemon unavailable"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("加载 worktree 失败");
  });
});
