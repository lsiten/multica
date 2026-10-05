import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { HumanRequest } from "@multica/core/types";
import { HumanRequestContent } from "./human-request-card";
import { renderWithI18n } from "../test/i18n";

const respond = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api: { respondHumanRequest: respond } }));

const request: HumanRequest = { id: "request", workspace_id: "workspace", source_task_id: "source", agent_id: "agent", recipient_id: "member", issue_id: "issue", chat_session_id: null, project_id: null, revision: 3, status: "pending", can_respond: true, expires_at: "2030-01-01T00:00:00Z", response: null, response_task_id: null, payload: { key: "approach", kind: "choice", title: "选择测试方式", steps: [], action_label: "提交选择", next: "智能体将按你选择的方式继续。", choices: [{ id: "unit", label: "仅运行单元测试", recommended: true }, { id: "full", label: "运行完整测试" }], details: "Long technical evidence" } };

function renderRequest(value: HumanRequest) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={client}><HumanRequestContent request={value} workspaceId="workspace" /></QueryClientProvider>, { locale: "zh-Hans" });
}

beforeEach(() => { respond.mockReset(); });

describe("human action controls", () => {
  it("sends the exact named choice and current revision", async () => {
    respond.mockResolvedValue({ ...request, status: "answered", response_task_id: "next-run" });
    renderRequest(request);
    expect(screen.getByText("Long technical evidence").closest("details")).not.toHaveAttribute("open");
    fireEvent.click(screen.getByRole("button", { name: "运行完整测试" }));
    await waitFor(() => expect(respond).toHaveBeenCalledWith("request", { revision: 3, decision: "choice", answer: "full" }));
  });

  it("keeps typed information after a failed send and names how to retry", async () => {
    respond.mockRejectedValue(new Error("offline"));
    renderRequest({ ...request, payload: { ...request.payload, kind: "input", choices: [], input_label: "测试站点地址", action_label: "提交地址" } });
    fireEvent.change(screen.getByLabelText("测试站点地址"), { target: { value: "https://test.example" } });
    fireEvent.click(screen.getByRole("button", { name: "提交地址" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("刷新此请求后重试"));
    expect(screen.getByLabelText("测试站点地址")).toHaveValue("https://test.example");
  });

  it("does not offer an approval to another reader or an unsupported kind", () => {
    const view = renderRequest({ ...request, can_respond: false });
    expect(screen.getByText("等待指定成员处理。")).toBeVisible();
    expect(screen.queryByRole("button", { name: "运行完整测试" })).not.toBeInTheDocument();
    view.unmount();
    renderRequest({ ...request, payload: { ...request.payload, kind: "unknown" } });
    expect(screen.queryByRole("button", { name: "提交选择" })).not.toBeInTheDocument();
  });

  it("reports manual completion for verification, rather than approving an operation", async () => {
    respond.mockResolvedValue({ ...request, status: "answered" });
    renderRequest({ ...request, payload: { ...request.payload, kind: "manual", choices: [], action_label: "重新检查权限", steps: ["在系统设置中开启屏幕录制权限"], verification: "Read the actual runtime permission" } });
    fireEvent.click(screen.getByRole("button", { name: "重新检查权限" }));
    await waitFor(() => expect(respond).toHaveBeenCalledWith("request", { revision: 3, decision: "completed" }));
  });
});
