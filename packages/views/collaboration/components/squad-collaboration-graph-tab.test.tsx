// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SquadCollaborationGraphResponse } from "@multica/core/types";
import { api, ApiError } from "@multica/core/api";
import { renderWithI18n } from "../../test/i18n";
import { SquadCollaborationGraphTab } from "./squad-collaboration-graph-tab";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: { updateSquadCollaborationGraph: vi.fn(), getSquadCollaborationGraph: vi.fn() },
}));
const original: SquadCollaborationGraphResponse = { squad_id: "squad", revision: 1, updated_at: null, relations: [{ id: "relation", from_member_id: "a", to_member_id: "b", from_member_type: "agent", to_member_type: "agent", type: "handoff", label: "Original", trigger: "", deliverables: [], acceptance: "" }], members: [{ member_id: "a", member_type: "agent", label: "Agent A", role: "" }, { member_id: "b", member_type: "agent", label: "Agent B", role: "" }] };
function harness(graph = original, canManage = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const dirty = vi.fn();
  const node = (nextGraph: SquadCollaborationGraphResponse) => <QueryClientProvider client={client}><SquadCollaborationGraphTab squadId="squad" graph={nextGraph} canManage={canManage} onDirtyChange={dirty} /></QueryClientProvider>;
  const view = renderWithI18n(node(graph), { locale: "zh-Hans" });
  return { ...view, dirty, refresh: (graph: SquadCollaborationGraphResponse) => view.rerender(node(graph)) };
}
describe("squad graph editing", () => {
  beforeEach(() => vi.clearAllMocks());
  it("keeps the edit base revision across remote refresh and retains a conflicted draft", async () => {
    vi.mocked(api.updateSquadCollaborationGraph).mockRejectedValue(new ApiError("conflict", 409, "Conflict"));
    const view = harness();
    fireEvent.change(screen.getByDisplayValue("Original"), { target: { value: "My draft" } });
    view.refresh({ ...original, revision: 2, relations: [{ ...original.relations[0]!, label: "Other editor" }] });
    expect(screen.getByDisplayValue("My draft")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "保存约定" }));
    await waitFor(() => expect(api.updateSquadCollaborationGraph).toHaveBeenCalledWith("squad", expect.objectContaining({ expected_revision: 1, relations: [expect.objectContaining({ label: "My draft" })] })));
    await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
    expect(screen.getByDisplayValue("My draft")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "放弃草稿并加载最新版本" }));
    expect(screen.getByDisplayValue("Other editor")).toBeInTheDocument();
  });
  it("freezes edits during save and sends deliverables", async () => {
    let finish: (value: SquadCollaborationGraphResponse) => void = () => {};
    vi.mocked(api.updateSquadCollaborationGraph).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    harness();
    fireEvent.change(screen.getByLabelText("交付物（每行一项）"), { target: { value: " Report.pdf\nChecks.json " } });
    fireEvent.click(screen.getByRole("button", { name: "保存约定" }));
    await waitFor(() => expect(screen.getByDisplayValue("Original")).toBeDisabled());
    expect(api.updateSquadCollaborationGraph).toHaveBeenLastCalledWith("squad", expect.objectContaining({ relations: [expect.objectContaining({ deliverables: ["Report.pdf", "Checks.json"] })] }));
    finish({ ...original, revision: 2 });
    await waitFor(() => expect(screen.getByDisplayValue("Original")).not.toBeDisabled());
  });
  it("opens a read-only historical snapshot without dropping the current draft", async () => {
    vi.mocked(api.getSquadCollaborationGraph).mockResolvedValue({ ...original, relations: [{ ...original.relations[0]!, label: "Archived", deliverables: ["old.pdf"] }] });
    harness();
    fireEvent.change(screen.getByDisplayValue("Original"), { target: { value: "Draft" } });
    fireEvent.change(screen.getByLabelText("历史版本"), { target: { value: "1" } });
    await screen.findByText("old.pdf");
    expect(api.getSquadCollaborationGraph).toHaveBeenCalledWith("squad", 1);
    expect(screen.queryByRole("button", { name: "保存约定" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "当前版本" }));
    expect(screen.getByDisplayValue("Draft")).toBeInTheDocument();
  });
  it("explains why a viewer cannot edit squad agreements", () => {
    harness(original, false);
    expect(screen.getByText("当前账号只能查看小队协作约定，只有小队创建者或工作区管理员可以编辑。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "新增关系" })).not.toBeInTheDocument();
  });
  it("locates the editor from a keyboard-selected graph relation without showing runtime evidence", () => {
    harness();
    const edge = screen.getByRole("button", {name:"交付 · 1"});
    expect(edge).toBeDefined();
    fireEvent.keyDown(edge!, { key: "Enter" });
    expect(screen.getByLabelText("关系名称")).toHaveFocus();
    expect(screen.queryByText("运行证据")).not.toBeInTheDocument();
  });
  it("keeps the add action disabled until the squad has two members", () => {
    harness({ ...original, members: [original.members[0]!] });
    expect(screen.getByRole("button", { name: "新增关系" })).toBeDisabled();
    expect(screen.getByText("至少需要 2 名小队成员才能新增协作关系。")).toBeInTheDocument();
  });
  it("adds a different direction and blocks duplicate agreements before saving", () => {
    harness();
    fireEvent.click(screen.getByRole("button", { name: "新增关系" }));
    const from = screen.getAllByLabelText("发起成员")[1]!;
    const to = screen.getAllByLabelText("接收成员")[1]!;
    expect(from).toHaveValue("agent:b");
    expect(to).toHaveValue("agent:a");
    expect(to.querySelector('option[value="agent:b"]')).toBeDisabled();
    fireEvent.change(to, { target: { value: "agent:b" } });
    fireEvent.change(from, { target: { value: "agent:a" } });
    expect(screen.getByRole("button", { name: "保存约定" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("同一方向和类型的关系不能重复");
  });
  it("preserves a failed draft and offers retry without misreporting a conflict", async () => {
    vi.mocked(api.updateSquadCollaborationGraph).mockRejectedValue(new ApiError("unavailable", 503, "Service Unavailable"));
    harness();
    fireEvent.change(screen.getByDisplayValue("Original"), { target: { value: "My draft" } });
    fireEvent.click(screen.getByRole("button", { name: "保存约定" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("保存失败，草稿已保留，请稍后重试。");
    expect(screen.getByDisplayValue("My draft")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存约定" })).toBeEnabled();
  });
});
