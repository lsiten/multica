// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../locales/en/agents.json";
import { AgentCollaborationControls } from "./agent-collaboration-controls";
import type { AgentTask } from "@multica/core/types/agent";

const mocks = vi.hoisted(() => ({
  append: vi.fn().mockResolvedValue({ status: "accepted" }),
  next: vi.fn().mockResolvedValue({ status: "accepted" }),
  delegate: vi.fn().mockResolvedValue({ status: "accepted" }),
  watch: vi.fn().mockResolvedValue({ status: "watching" }),
}));
vi.mock("@multica/core/api", () => ({ api: { agentCollaboration: mocks } }));

const task = { id: "run-1", agent_id: "agent-1", issue_id: "issue-1", status: "running" } as AgentTask;

function renderControls() {
  return render(
    <I18nProvider locale="en" resources={{ en: { agents: enAgents } }}>
      <AgentCollaborationControls task={task} />
    </I18nProvider>,
  );
}

describe("AgentCollaborationControls", () => {
  it("keeps append and next as explicit operations", async () => {
    renderControls();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "append" } });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "追加检查" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(mocks.append).toHaveBeenCalledWith("run-1", expect.objectContaining({ content: "追加检查" })));

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "next" } });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "下一轮继续" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(mocks.next).toHaveBeenCalledWith("run-1", expect.objectContaining({ content: "下一轮继续" })));
  });

  it("registers a terminal watch without sending content", async () => {
    renderControls();
    fireEvent.click(screen.getByRole("button", { name: "Watch result" }));
    await waitFor(() => expect(mocks.watch).toHaveBeenCalledWith("run-1", expect.any(String)));
  });

  it("requires and sends an explicit target Agent for delegation", async () => {
    renderControls();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "delegate" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Target Agent" }), { target: { value: "agent-2" } });
    fireEvent.change(screen.getByRole("textbox", { name: /Describe the agent/ }), { target: { value: "独立审查" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => expect(mocks.delegate).toHaveBeenCalledWith("issue-1", expect.objectContaining({ targetAgentId: "agent-2", content: "独立审查" })));
  });
});
