import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { HumanRequest } from "@multica/core/types";
import { humanRequestKeys } from "@multica/core/human-requests";
import { HumanRequestCard, HumanRequestContent } from "./human-request-card";
import { renderWithI18n } from "../test/i18n";

const { respond, getRequest } = vi.hoisted(() => ({ respond: vi.fn(), getRequest: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { respondHumanRequest: respond, getHumanRequest: getRequest } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/realtime", () => ({ useWSEvent: vi.fn(), useWSReconnect: vi.fn() }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/paths")>(),
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}`, chatSession: (id: string) => `/acme/chat?session=${encodeURIComponent(id)}` }),
}));
vi.mock("../navigation", () => ({ AppLink: ({ href, children, ...props }: { href: string; children: ReactNode }) => <a href={href} {...props}>{children}</a> }));

const request: HumanRequest = { id: "request", workspace_id: "workspace", source_task_id: "source", agent_id: "agent", recipient_id: "member", issue_id: "issue", chat_session_id: null, project_id: null, revision: 3, status: "pending", can_respond: true, expires_at: "2030-01-01T00:00:00Z", response: null, response_task_id: null, payload: { key: "approach", kind: "choice", title: "选择测试方式", steps: [], action_label: "提交选择", next: "智能体将按你选择的方式继续。", choices: [{ id: "unit", label: "仅运行单元测试", recommended: true }, { id: "full", label: "运行完整测试" }], details: "Long technical evidence" } };

function renderRequest(value: HumanRequest) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={client}><HumanRequestContent request={value} workspaceId="workspace" /></QueryClientProvider>, { locale: "zh-Hans" });
}

beforeEach(() => { respond.mockReset(); getRequest.mockReset(); });

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

  it("exposes links back to the originating issue and chat", () => {
    renderRequest({ ...request, chat_session_id: "chat-1" });
    expect(screen.getByRole("link", { name: "打开关联工作项" })).toHaveAttribute("href", "/acme/issues/issue");
    expect(screen.getByRole("link", { name: "打开关联聊天" })).toHaveAttribute("href", "/acme/chat?session=chat-1");
  });

  it("reports manual completion for verification, rather than approving an operation", async () => {
    respond.mockResolvedValue({ ...request, status: "answered" });
    renderRequest({ ...request, payload: { ...request.payload, kind: "manual", choices: [], action_label: "重新检查权限", steps: ["在系统设置中开启屏幕录制权限"], verification: "Read the actual runtime permission" } });
    fireEvent.click(screen.getByRole("button", { name: "重新检查权限" }));
    await waitFor(() => expect(respond).toHaveBeenCalledWith("request", { revision: 3, decision: "completed" }));
  });
});

describe("human request revision refresh", () => {
  const inputRequest: HumanRequest = { ...request, payload: { ...request.payload, kind: "input", choices: [], input_label: "测试站点地址", action_label: "提交地址" } };

  function renderCard() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const view = renderWithI18n(<QueryClientProvider client={client}><HumanRequestCard requestId="request" /></QueryClientProvider>, { locale: "zh-Hans" });
    return { ...view, client };
  }

  it("retains free text after a conflict refresh and submits the displayed revision only on another click", async () => {
    const revised = { ...inputRequest, revision: 4, payload: { ...inputRequest.payload, title: "确认更新后的测试地址", impact: "仅更新测试环境" } };
    getRequest.mockResolvedValue(inputRequest);
    respond.mockRejectedValueOnce(new Error("revision conflict"));
    renderCard();
    fireEvent.change(await screen.findByLabelText("测试站点地址"), { target: { value: "https://test.example" } });
    getRequest.mockResolvedValue(revised);

    fireEvent.click(screen.getByRole("button", { name: "提交地址" }));

    await screen.findByRole("heading", { name: revised.payload.title });
    expect(screen.getByText(revised.payload.impact)).toBeVisible();
    expect(screen.getByLabelText("测试站点地址")).toHaveValue("https://test.example");
    expect(respond).toHaveBeenCalledExactlyOnceWith("request", { revision: 3, decision: "input", answer: "https://test.example" });
    fireEvent.click(screen.getByRole("button", { name: "刷新请求" }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(screen.getByLabelText("测试站点地址")).toHaveValue("https://test.example");
    respond.mockResolvedValue({ ...revised, status: "answered", response: { revision: 4, decision: "input", answer: "https://test.example" } });
    getRequest.mockResolvedValue({ ...revised, status: "answered" });
    fireEvent.click(screen.getByRole("button", { name: "提交地址" }));
    await waitFor(() => expect(respond).toHaveBeenLastCalledWith("request", { revision: 4, decision: "input", answer: "https://test.example" }));
    await screen.findByText("已收到回复，后续运行已排队。");
  });

  it.each([
    ["request identity", { id: "another-request" }],
    ["request type", { payload: { ...inputRequest.payload, kind: "confirmation" as const } }],
    ["input purpose", { payload: { ...inputRequest.payload, key: "another-input" } }],
    ["recipient", { recipient_id: "another-member" }],
  ])("clears free text when %s changes", async (_name, change) => {
    getRequest.mockResolvedValue(inputRequest);
    const { client } = renderCard();
    fireEvent.change(await screen.findByLabelText("测试站点地址"), { target: { value: "private draft" } });

    const changed = { ...inputRequest, ...change, revision: 4 };
    act(() => { client.setQueryData(humanRequestKeys.detail("workspace", "request"), { ...changed, payload: { ...changed.payload, title: "Changed request" } }); });
    await screen.findByRole("heading", { name: "Changed request" });
    act(() => { client.setQueryData(humanRequestKeys.detail("workspace", "request"), { ...inputRequest, revision: 5 }); });

    await waitFor(() => expect(screen.getByLabelText("测试站点地址")).toHaveValue(""));
    expect(respond).not.toHaveBeenCalled();
  });

  it.each(["choice", "confirmation", "manual"] as const)("requires another explicit decision after a %s revision conflict", async kind => {
    const original = { ...request, payload: { ...request.payload, kind } };
    const revised = { ...original, revision: 4, payload: { ...original.payload, title: "Updated operation" } };
    getRequest.mockResolvedValue(original);
    respond.mockRejectedValue(new Error("revision conflict"));
    renderCard();
    await screen.findByRole("heading", { name: original.payload.title });
    getRequest.mockResolvedValue(revised);

    fireEvent.click(screen.getByRole("button", { name: kind === "choice" ? "运行完整测试" : "提交选择" }));

    await screen.findByRole("heading", { name: revised.payload.title });
    expect(respond).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
});
