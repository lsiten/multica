import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatReferenceDraftStore } from "@multica/core/chat";
import type { Agent, ChatMessage } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ChatMessageActions } from "./chat-message-actions";
import { useChatMessageActions } from "./use-chat-message-actions";

const agent: Agent = {
  id: "agent", workspace_id: "workspace", runtime_id: "runtime", name: "Fixture", description: "", instructions: "", avatar_url: null,
  runtime_mode: "local", runtime_config: {}, custom_args: [], visibility: "private", permission_mode: "private", invocation_targets: [],
  status: "idle", max_concurrent_tasks: 1, model: "", owner_id: "member", skills: [], created_at: "", updated_at: "", archived_at: null, archived_by: null,
};
const message: ChatMessage = { id: "answer", chat_session_id: "original-chat", role: "assistant", task_id: null, content: "Original answer snapshot", created_at: "2026-11-15T13:00:00Z" };
const originalScope = JSON.stringify(["workspace", "original-chat"]);
const draftScope = JSON.stringify(["workspace", null]);
const newChat = vi.fn();
const focus = vi.fn();

function Surface() {
  const actions = useChatMessageActions({ wsId: "workspace", activeSessionId: "original-chat", activeAgent: agent,
    isAgentArchived: false, isAgentAccessRevoked: false, isSessionArchived: false, handleStartNewChat: newChat, requestInputFocus: focus });
  return <><ChatMessageActions message={message} {...actions} />{actions.dialog}</>;
}

beforeEach(() => {
  useChatReferenceDraftStore.setState({ byScope: {} });
  newChat.mockReset();
  focus.mockReset();
});

describe("message quote and discussion drafts", () => {
  it("quotes a whole message once when the same source snapshot is selected again", () => {
    renderWithI18n(<Surface />);
    fireEvent.click(screen.getByRole("button", { name: "Quote message" }));
    fireEvent.click(screen.getByRole("button", { name: "Quote message" }));
    expect(useChatReferenceDraftStore.getState().byScope[originalScope]).toMatchObject([{
      content: "Original answer snapshot", messageSource: { sessionId: "original-chat", messageId: "answer", role: "assistant" },
    }]);
    expect(focus).toHaveBeenCalledTimes(2);
    expect(newChat).not.toHaveBeenCalled();
  });

  it("adds the selected snapshot to a new discussion draft without consuming either draft's references", () => {
    useChatReferenceDraftStore.getState().add(originalScope, { id: "original-reference", label: "Original context", content: "Keep original context" });
    useChatReferenceDraftStore.getState().add(draftScope, { id: "draft-reference", label: "Draft context", content: "Keep new-chat context" });
    renderWithI18n(<Surface />);
    fireEvent.click(screen.getByRole("button", { name: "Continue in new discussion" }));
    fireEvent.click(screen.getByRole("button", { name: "Open discussion draft" }));

    expect(useChatReferenceDraftStore.getState().byScope[draftScope]?.map((reference) => reference.content)).toEqual(["Keep new-chat context", "Original answer snapshot"]);
    expect(useChatReferenceDraftStore.getState().byScope[originalScope]?.map((reference) => reference.id)).toEqual(["original-reference"]);
    expect(newChat).toHaveBeenCalledExactlyOnceWith(agent);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("leaves both conversations untouched when the new-discussion prompt is canceled", () => {
    renderWithI18n(<Surface />);
    fireEvent.click(screen.getByRole("button", { name: "Continue in new discussion" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(useChatReferenceDraftStore.getState().byScope).toEqual({});
    expect(newChat).not.toHaveBeenCalled();
  });
});
