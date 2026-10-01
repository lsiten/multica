import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SquadCollaborationGraphResponse } from "@multica/core/types";
import { api } from "@multica/core/api";
import { renderWithI18n } from "../../test/i18n";
import { SquadCollaborationGraphTab } from "./squad-collaboration-graph-tab";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/api", () => ({ api: { updateSquadCollaborationGraph: vi.fn(), getSquadCollaborationGraph: vi.fn() } }));
const original: SquadCollaborationGraphResponse = { squad_id: "squad", revision: 1, updated_at: null, relations: [{ id: "relation", from_member_id: "a", to_member_id: "b", from_member_type: "agent", to_member_type: "agent", type: "handoff", label: "Original", trigger: "", deliverables: [], acceptance: "" }], members: [{ member_id: "a", member_type: "agent", label: "Agent A", role: "" }, { member_id: "b", member_type: "agent", label: "Agent B", role: "" }] };
function harness() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const dirty = vi.fn();
  const node = (graph: SquadCollaborationGraphResponse) => <QueryClientProvider client={client}><SquadCollaborationGraphTab squadId="squad" graph={graph} canManage onDirtyChange={dirty} /></QueryClientProvider>;
  const view = renderWithI18n(node(original), { locale: "zh-Hans" });
  return { ...view, dirty, refresh: (graph: SquadCollaborationGraphResponse) => view.rerender(node(graph)) };
}
describe("squad graph editing", () => {
  it("keeps the edit base revision across remote refresh and retains a conflicted draft", async () => {
    vi.mocked(api.updateSquadCollaborationGraph).mockRejectedValue(new Error("conflict"));
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
});
