import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChatMessage, TaskMessagePayload } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "../../../test/i18n";
import { MirrorChatOverlay } from "./mirror-chat-overlay";

vi.mock("@multica/core/chat/queries", () => ({
  isTaskMessageTaskId: (id: unknown) => typeof id === "string" && id.length > 0,
  taskMessagesOptions: (id: string) => ({ queryKey: ["task", id], queryFn: async () => [], staleTime: Infinity }),
}));

function message(id: string, text: string): ChatMessage {
  return { id, content: text, role: "user", task_id: null, chat_session_id: "chat", created_at: "2026-09-21T00:00:00Z" };
}

function view(client: QueryClient, messages: ChatMessage[], pendingId?: string) {
  return <I18nProvider locale="en" resources={RESOURCES}>
    <QueryClientProvider client={client}>
      <MirrorChatOverlay agentName="Mika" messages={messages} pendingTask={pendingId ? { task_id: pendingId } : null} />
    </QueryClientProvider>
  </I18nProvider>;
}

describe("MirrorChatOverlay", () => {
  it("keeps bottom-aligned history with a fading edge and no chat panel controls", async () => {
    const client = new QueryClient();
    const messages = Array.from({ length: 8 }, (_, i) => message(String(i), `Message ${i}`));
    const { rerender } = render(view(client, messages));
    expect(screen.getAllByRole("listitem")).toHaveLength(8);
    const overlay = screen.getByRole("complementary", { name: "Live conversation" });
    expect(overlay).toHaveClass("overflow-y-auto", "overscroll-contain", "bottom-3", "right-3");
    expect(screen.getByRole("list")).toHaveClass("justify-end");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    rerender(view(client, [...messages, message("new", "New reply")]));
    expect(screen.getAllByRole("listitem").at(-1)).toHaveTextContent("New reply");
    expect(screen.getByText("Message 0", { exact: false })).toBeInTheDocument();
    Object.defineProperties(overlay, { scrollHeight: { value: 600, configurable: true }, clientHeight: { value: 160 } });
    overlay.scrollTop = 440;
    fireEvent.scroll(overlay);
    await waitFor(() => expect(overlay.style.maskImage).toContain("transparent 0%"));
    fireEvent.wheel(overlay, { deltaY: -200 });
    overlay.scrollTop = 150;
    fireEvent.scroll(overlay);
    rerender(view(client, [...messages, message("later", "Later reply")]));
    expect(overlay.scrollTop).toBe(150);
    overlay.scrollTop = 440;
    fireEvent.scroll(overlay);
    Object.defineProperty(overlay, "scrollHeight", { value: 700 });
    rerender(view(client, [...messages, message("latest", "Latest reply")]));
    expect(overlay.scrollTop).toBe(700);
  });

  it("shows streamed reply text and replaces it with the persisted answer without duplication", () => {
    const client = new QueryClient();
    const live: TaskMessagePayload[] = [
      { task_id: "run", issue_id: "issue", seq: 1, type: "text", content: "Working " },
      { task_id: "run", issue_id: "issue", seq: 2, type: "text", content: "on it" },
      { task_id: "run", issue_id: "issue", seq: 3, type: "thinking", content: "Private reasoning" },
    ];
    client.setQueryData(["task", "run"], live);
    const { rerender } = render(view(client, [], "run"));
    expect(screen.getByRole("listitem")).toHaveTextContent("Mika: Working on it");
    expect(screen.queryByText("Private reasoning")).not.toBeInTheDocument();
    rerender(view(client, [{ ...message("done", "Completed"), role: "assistant", task_id: "run" }], "run"));
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
    expect(screen.getByRole("listitem")).toHaveTextContent("Mika: Completed");
  });

  it("omits empty messages and hidden onboarding prompts", () => {
    render(view(new QueryClient(), [message("empty", ""), { ...message("hidden", "Internal prompt"), message_kind: "onboarding_kickoff" }]));
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });
});
