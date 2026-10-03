// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { renderWithI18n } from "../../test/i18n";
import { JevDecisionLogsTab } from "./jev-decision-logs-tab";

const scope = vi.hoisted(() => ({ workspace: "workspace-1", role: "owner" }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ id: scope.workspace, name: "Dev" }) }));
vi.mock("@multica/core/permissions", () => ({ useCurrentMember: () => ({ role: scope.role }) }));
vi.mock("@multica/core/api", () => ({ api: { listJevDecisionLogs: vi.fn(), getJevDecisionLog: vi.fn() } }));
const summary = { id: "decision-1", workspace_id: "workspace-1", task_id: "task-1", agent_id: "agent-1", agent_name: "审核者", issue_identifier: "MUL-123", source: "local", tool: "multica_jev_systemone", model: "Mapika/decider-2b", result_class: "success", error_code: "", duration_ms: 123, started_at: "2026-10-03T12:00:00Z" } as const;
const page = { items: [summary], total: 21, limit: 20, offset: 0, as_of: "2026-10-03T12:01:00Z" };
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = <QueryClientProvider client={client}><JevDecisionLogsTab /></QueryClientProvider>;
  return { ...renderWithI18n(view, { locale: "zh-Hans" }), client };
}
beforeEach(() => {
  vi.clearAllMocks(); scope.workspace = "workspace-1"; scope.role = "owner";
  vi.mocked(api.listJevDecisionLogs).mockResolvedValue(page);
  vi.mocked(api.getJevDecisionLog).mockResolvedValue({ ...summary, model_revision: "pinned", config_revision: 1, device: "cpu", completed_at: "2026-10-03T12:00:01Z", input: '{"question":"报告是否完成？"}', output: '{"verdict":"yes"}', requests: [{ attempt: 1, variant: "systemone", http_status: 200, result_class: "success", duration_ms: 123, input: '{"state":"report"}', output: '{"answers":{"ready":true}}', response_incomplete: false }] });
});
describe("Jev decision log page", () => {
  it("pages on the server with the same snapshot and resets pagination after searching", async () => {
    const user = userEvent.setup(); mount();
    await screen.findByText("MUL-123");
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.listJevDecisionLogs).toHaveBeenLastCalledWith("workspace-1", expect.objectContaining({ offset: 20, limit: 20, as_of: page.as_of })));
    await user.type(screen.getByRole("searchbox", { name: "搜索" }), "report");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listJevDecisionLogs).toHaveBeenLastCalledWith("workspace-1", expect.objectContaining({ q: "report", offset: 0 })));
  });
  it("applies result, source, Agent and date filters and clears them", async () => {
    const user = userEvent.setup(); mount(); await screen.findByText("MUL-123");
    await user.selectOptions(screen.getByRole("combobox", { name: "结果" }), "error");
    await user.selectOptions(screen.getByRole("combobox", { name: "模型来源" }), "remote");
    await user.type(screen.getByRole("textbox", { name: "智能体" }), "Reviewer");
    fireEvent.change(screen.getByLabelText("开始日期"), { target: { value: "2026-10-01" } });
    fireEvent.change(screen.getByLabelText("结束日期"), { target: { value: "2026-10-03" } });
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listJevDecisionLogs).toHaveBeenLastCalledWith("workspace-1", expect.objectContaining({ result_class: "error", source: "remote", agent: "Reviewer", from: expect.any(String), to: expect.any(String) })));
    await user.click(screen.getByRole("button", { name: "清空筛选" }));
    await waitFor(() => expect(api.listJevDecisionLogs).toHaveBeenLastCalledWith("workspace-1", expect.objectContaining({ result_class: "", source: "", agent: "" })));
  });
  it("loads one decision with input, output and raw model attempts", async () => {
    const user = userEvent.setup(); mount();
    await user.click(await screen.findByRole("button", { name: "查看决策 decision-1" }));
    const dialog = await screen.findByRole("dialog", { name: "决策详情" });
    await waitFor(() => expect(dialog).toHaveTextContent("报告是否完成？"));
    expect(api.getJevDecisionLog).toHaveBeenCalledWith("workspace-1", "decision-1");
    expect(dialog).toHaveTextContent("task-1");
    await user.click(screen.getByText(/第 1 次请求/));
    expect(dialog).toHaveTextContent("原始响应");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
  it("shows loading failures with retry and an empty state after recovery", async () => {
    vi.mocked(api.listJevDecisionLogs).mockRejectedValueOnce(new Error("offline"));
    const user = userEvent.setup(); mount();
    expect(await screen.findByRole("alert")).toHaveTextContent("无法加载决策日志");
    vi.mocked(api.listJevDecisionLogs).mockResolvedValue({ ...page, total: 0, items: [] });
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("暂无决策日志")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
  });
  it("does not query logs for members without administration rights", () => {
    scope.role = "member"; mount();
    expect(api.listJevDecisionLogs).not.toHaveBeenCalled();
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });
  it("starts fresh when the workspace changes", async () => {
    const user = userEvent.setup(); const view = mount(); await screen.findByText("MUL-123");
    await user.type(screen.getByRole("searchbox", { name: "搜索" }), "private workspace query");
    scope.workspace = "workspace-2";
    view.rerender(<QueryClientProvider client={view.client}><JevDecisionLogsTab /></QueryClientProvider>);
    await waitFor(() => expect(api.listJevDecisionLogs).toHaveBeenLastCalledWith("workspace-2", expect.objectContaining({ q: "", offset: 0 })));
    expect(screen.getByRole("searchbox", { name: "搜索" })).toHaveValue("");
  });
});
