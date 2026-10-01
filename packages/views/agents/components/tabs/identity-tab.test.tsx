// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { api } from "@multica/core/api";
import { agentIdentityKeys } from "@multica/core/agents/queries";
import type { Agent, AgentIdentity } from "@multica/core/types";
import { toast } from "sonner";
import { renderWithI18n } from "../../../test/i18n";
import { IdentityTab } from "./identity-tab";

const workspace = vi.hoisted(() => ({ id: "ws-1" }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => workspace.id }));
vi.mock("@multica/core/api", () => ({ api: {
  getAgentIdentity: vi.fn(), updateAgentIdentity: vi.fn(),
} }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const identity: AgentIdentity = {
  agent_id: "agent-1", email: "original@example.com", phone: "+12025550123",
};
const agent: Agent = {
  id: "agent-1", workspace_id: "ws-1", runtime_id: "runtime-1", name: "Test agent",
  description: "", instructions: "", avatar_url: null, runtime_mode: "local",
  runtime_config: {}, custom_args: [], visibility: "private", permission_mode: "private",
  invocation_targets: [], status: "idle", max_concurrent_tasks: 1, model: "", owner_id: null,
  skills: [], created_at: "", updated_at: "", archived_at: null, archived_by: null,
};
let queryClient: QueryClient;
const key = agentIdentityKeys.detail("ws-1", "agent-1");

function renderIdentity(props: Partial<React.ComponentProps<typeof IdentityTab>> = {}) {
  const onDirtyChange = vi.fn();
  const ui = (next = props) => (
    <QueryClientProvider client={queryClient}>
      <IdentityTab agent={agent} canEdit onDirtyChange={onDirtyChange} {...next} />
    </QueryClientProvider>
  );
  const result = renderWithI18n(ui());
  return { onDirtyChange, rerender: (next: typeof props) => result.rerender(ui(next)) };
}

beforeEach(() => {
  vi.resetAllMocks();
  workspace.id = "ws-1";
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  vi.mocked(api.getAgentIdentity).mockResolvedValue(identity);
});
afterEach(() => { cleanup(); queryClient.clear(); });

describe("IdentityTab", () => {
  it("shows a retry action without an editable empty form when loading fails", async () => {
    vi.mocked(api.getAgentIdentity).mockRejectedValueOnce(new Error("network error"));
    renderIdentity();

    await screen.findByRole("alert");

    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save identity" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
  });

  it("loads the saved identity after retrying a failed request", async () => {
    vi.mocked(api.getAgentIdentity).mockRejectedValueOnce(new Error("network error"));
    renderIdentity();
    const retry = await screen.findByRole("button", { name: "Retry" });

    fireEvent.click(retry);

    expect(await screen.findByLabelText("Email address")).toHaveValue(identity.email);
    expect(screen.getByRole("button", { name: "Save identity" })).toBeDisabled();
  });

  it("preserves unsaved edits when a background request returns newer server data", async () => {
    const { onDirtyChange } = renderIdentity();
    fireEvent.change(await screen.findByLabelText("Email address"), { target: { value: "draft@example.com" } });
    vi.mocked(api.getAgentIdentity).mockResolvedValue({ ...identity, email: "remote@example.com" });

    await act(() => queryClient.refetchQueries({ queryKey: key }));

    expect(screen.getByLabelText("Email address")).toHaveValue("draft@example.com");
    expect(screen.getByRole("button", { name: "Save identity" })).toBeEnabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
  });

  it("uses the PUT-confirmed values and cache without requiring another GET", async () => {
    const confirmed = { ...identity, email: "normalized@example.com" };
    vi.mocked(api.updateAgentIdentity).mockResolvedValue(confirmed);
    const { onDirtyChange } = renderIdentity();
    fireEvent.change(await screen.findByLabelText("Email address"), { target: { value: " draft@example.com " } });
    vi.mocked(api.getAgentIdentity).mockRejectedValue(new Error("subsequent GET unavailable"));

    fireEvent.click(screen.getByRole("button", { name: "Save identity" }));

    await waitFor(() => expect(toast.success).toHaveBeenCalledOnce());
    expect(screen.getByLabelText("Email address")).toHaveValue(confirmed.email);
    expect(queryClient.getQueryData(key)).toEqual(confirmed);
    expect(api.getAgentIdentity).toHaveBeenCalledTimes(1);
    expect(api.updateAgentIdentity).toHaveBeenCalledWith("agent-1", {
      email: "draft@example.com", phone: identity.phone,
    });
    expect(screen.getByRole("button", { name: "Save identity" })).toBeDisabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("retains the draft and permits retry when the PUT fails", async () => {
    vi.mocked(api.updateAgentIdentity).mockRejectedValue(new Error("save unavailable"));
    renderIdentity();
    fireEvent.change(await screen.findByLabelText("Email address"), { target: { value: "draft@example.com" } });

    fireEvent.click(screen.getByRole("button", { name: "Save identity" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledOnce());
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Email address")).toHaveValue("draft@example.com");
    expect(screen.getByRole("button", { name: "Save identity" })).toBeEnabled();
    expect(queryClient.getQueryData(key)).toEqual(identity);
  });

  it("starts with a clean draft after switching agents", async () => {
    const view = renderIdentity();
    fireEvent.change(await screen.findByLabelText("Email address"), { target: { value: "old-draft@example.com" } });
    vi.mocked(api.getAgentIdentity).mockResolvedValue({ ...identity, agent_id: "agent-2", email: "agent2@example.com" });

    view.rerender({ agent: { ...agent, id: "agent-2" } });

    expect(await screen.findByLabelText("Email address")).toHaveValue("agent2@example.com");
    expect(view.onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(screen.getByRole("button", { name: "Save identity" })).toBeDisabled();
  });

  it("isolates cached values and drafts after switching workspaces", async () => {
    const view = renderIdentity();
    fireEvent.change(await screen.findByLabelText("Email address"), { target: { value: "old-draft@example.com" } });
    const otherIdentity = { ...identity, email: "workspace2@example.com" };
    vi.mocked(api.getAgentIdentity).mockResolvedValue(otherIdentity);
    workspace.id = "ws-2";

    view.rerender({});

    expect(await screen.findByLabelText("Email address")).toHaveValue(otherIdentity.email);
    expect(view.onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(queryClient.getQueryData(key)).toEqual(identity);
    expect(queryClient.getQueryData(agentIdentityKeys.detail("ws-2", "agent-1"))).toEqual(otherIdentity);
  });

  it("keeps a view-only identity uneditable", async () => {
    renderIdentity({ canEdit: false });

    await screen.findByLabelText("Email address");

    for (const input of screen.getAllByRole("textbox")) expect(input).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save identity" })).not.toBeInTheDocument();
  });
});
