// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChatSession } from "@multica/core/types";
import enChat from "../../locales/en/chat.json";

const listParticipants = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: { listChatGroupParticipants: listParticipants },
}));

vi.mock("@multica/core/chat/mutations", () => ({
  useAddChatGroupParticipant: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_type: string, id: string) => id }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

import { GroupChatParticipants } from "./group-chat-participants";

const session: ChatSession = {
  id: "session-1",
  workspace_id: "ws-1",
  agent_id: "lead-agent",
  creator_id: "user-1",
  kind: "group",
  title: "",
  status: "active",
  has_unread: false,
  participants: [],
  created_at: "2026-01-01",
  updated_at: "2026-01-01",
};

function renderParticipants() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
        <GroupChatParticipants session={session} agents={[]} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("GroupChatParticipants", () => {
  beforeEach(() => {
    listParticipants.mockReset();
  });

  it("uses the server participant projection even when the session cache is empty", async () => {
    listParticipants.mockResolvedValue([
      { member_type: "member", member_id: "user-1", joined_at: "" },
      { member_type: "agent", member_id: "agent-2", joined_at: "" },
    ]);

    renderParticipants();

    expect(await screen.findByText("2")).toBeInTheDocument();
    expect(listParticipants).toHaveBeenCalledWith("session-1");
  });
});
