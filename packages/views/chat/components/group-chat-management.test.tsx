// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, ChatSession } from "@multica/core/types";
import enChat from "../../locales/en/chat.json";

const mocks = vi.hoisted(() => ({
  update: vi.fn(),
  add: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("@multica/core/chat/mutations", () => ({
  useUpdateChatSession: () => ({ mutate: mocks.update, isPending: false }),
  useAddChatGroupParticipant: () => ({ mutate: mocks.add, mutateAsync: mocks.add, isPending: false }),
  useRemoveChatGroupParticipant: () => ({ mutate: mocks.remove, isPending: false }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_type: string, id: string) => ({ "agent-lead": "Lead", "agent-two": "Second", "agent-three": "Third" }[id] ?? id) }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid={`avatar-${actorId}`} />,
}));

import { GroupChatManagement } from "./group-chat-management";

const agent = (id: string, name: string): Agent => ({ id, name, owner_id: "user-1" } as Agent);
const session: ChatSession = {
  id: "session-1",
  workspace_id: "ws-1",
  agent_id: "agent-lead",
  creator_id: "user-1",
  kind: "group",
  title: "Release crew",
  status: "active",
  has_unread: false,
  participants: [
    { member_type: "agent", member_id: "agent-lead", joined_at: "" },
    { member_type: "agent", member_id: "agent-two", joined_at: "" },
  ],
  created_at: "2026-01-01",
  updated_at: "2026-01-01",
};

function renderManagement() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
        <GroupChatManagement session={session} agents={[agent("agent-lead", "Lead"), agent("agent-two", "Second"), agent("agent-three", "Third")]} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("GroupChatManagement", () => {
  beforeEach(() => {
    mocks.update.mockReset();
    mocks.add.mockReset().mockResolvedValue({ member_type: "agent", member_id: "agent-three", joined_at: "" });
    mocks.remove.mockReset();
  });

  it("edits the group name and keeps the lead protected", () => {
    renderManagement();
    expect(screen.getByText("Lead agent · Lead")).toBeInTheDocument();
    expect(screen.getByText("Lead agent")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove member" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit group name" }));
    const input = screen.getByRole("textbox", { name: "Group name" });
    fireEvent.change(input, { target: { value: "Launch crew" } });
    fireEvent.click(screen.getByRole("button", { name: "Save group name" }));
    expect(mocks.update).toHaveBeenCalledWith({ sessionId: "session-1", title: "Launch crew" });
  });

  it("selects and adds multiple available agents", async () => {
    renderManagement();
    fireEvent.click(screen.getByRole("button", { name: "Add agents" }));
    fireEvent.click(screen.getByRole("button", { name: /Third/ }));
    fireEvent.click(screen.getByRole("button", { name: /Add selected/ }));
    expect(mocks.add).toHaveBeenCalledWith({ sessionId: "session-1", memberId: "agent-three" });
  });
});
