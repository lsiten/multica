import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { IssueProgressViewSchema } from "@multica/core/api/issue-progress";
import { renderWithI18n } from "../../test/i18n";
import { WorkProgressActions } from "./work-progress-actions";

vi.mock("@multica/core/api", async () => ({ ...await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api"), api: { performProgressAction: vi.fn(), createComment: vi.fn() } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/workspace/hooks", () => ({ useActorName: () => ({ getActorName: () => "Me" }) }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({}) }));
vi.mock("./pickers/assignee-picker", () => ({ AssigneePicker: () => null }));

function renderAction(kind = "provide_info") {
  const entry = IssueProgressViewSchema.parse({ workspace_id: "workspace", scope: { type: "project", id: "project" }, as_of: "2026-10-05T00:00:00Z", version: "1", summary: { total: 1, done: 0, closed: 0, open: 1, open_leaf: 1, open_parent: 0, attention: 1, pending_decisions: 0 }, filtered_total: 1, complete: true, items: [{ issue: { id: "issue", identifier: "MUL-1", title: "Information", status: "in_progress" }, revision: 4, run: { id: "run", status: "completed", since: "2026-10-05T00:00:00Z", summary: "Delivery" }, next_step: { kind: "needs_information", summary: "Supply the URL and acceptance criteria", actor_type: "member", actor_id: "me", missing: ["Test URL", "Acceptance criteria"], issue_revision: 4 }, actions: [{ kind, actor_type: "member", actor_id: "me", needs_me: true, enabled: true, disabled_reason: "" }] }] }).items[0]!;
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return { ...renderWithI18n(<QueryClientProvider client={client}><WorkProgressActions entry={entry} /></QueryClientProvider>), entry, client };
}
beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.performProgressAction).mockResolvedValue({ issue_id: "issue", task_id: "next", status: "queued" }); });
afterEach(cleanup);

describe("attention actions", () => {
  it("keeps the inspected revision and draft when a newer run replaces an open operation", async () => {
    const {entry,client,rerender}=renderAction();await userEvent.click(screen.getByRole("button",{name:"Provide information"}));await userEvent.type(screen.getByRole("textbox",{name:"Test URL"}),"Saved URL");
    rerender(<QueryClientProvider client={client}><WorkProgressActions entry={{...entry,revision:5,run:{...entry.run!,id:"new-run"}}}/></QueryClientProvider>);
    expect(screen.getByRole("button",{name:"Submit operation"})).toBeDisabled();expect(screen.getByRole("textbox",{name:"Test URL"})).toHaveValue("Saved URL");expect(screen.getByRole("alert")).toHaveTextContent("Work changed");expect(api.performProgressAction).not.toHaveBeenCalled();
  });

  it("requires each missing field and submits distinct values against the observed revision", async () => {
    renderAction(); await userEvent.click(screen.getByRole("button", { name: "Provide information" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Test URL" }), "https://test.example");
    expect(screen.getByRole("button", { name: "Submit operation" })).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox", { name: "Acceptance criteria" }), "All four images");
    await userEvent.click(screen.getByRole("button", { name: "Submit operation" }));
    await waitFor(() => expect(api.performProgressAction).toHaveBeenCalledWith("issue", expect.objectContaining({ issue_revision: 4, run_id: "run", kind: "provide_info", text: "Test URL: https://test.example\nAcceptance criteria: All four images" })));
    expect(await screen.findByRole("status")).toHaveTextContent("Work queued");
  });
  it("retains field inputs after a stale action instead of submitting again automatically", async () => {
    vi.mocked(api.performProgressAction).mockRejectedValue(new ApiError("changed", 409, "Conflict"));
    renderAction(); await userEvent.click(screen.getByRole("button", { name: "Provide information" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Test URL" }), "URL");
    await userEvent.type(screen.getByRole("textbox", { name: "Acceptance criteria" }), "Criteria");
    await userEvent.click(screen.getByRole("button", { name: "Submit operation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Your input is retained");
    expect(screen.getByRole("textbox", { name: "Test URL" })).toHaveValue("URL");
    expect(api.performProgressAction).toHaveBeenCalledTimes(1);
  });
});
