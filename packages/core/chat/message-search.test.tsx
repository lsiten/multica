// @vitest-environment jsdom

import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import type { ChatMessage, ChatSession } from "../types";
import { useChatMessageSearch } from "./message-search";

vi.mock("../api", async (original) => ({
  ...await original<typeof import("../api")>(), api: { listChatMessagesPage: vi.fn() },
}));

const sessions: ChatSession[] = ["one", "two"].map((id) => ({
  id, workspace_id: "ws", agent_id: "agent", creator_id: "user", title: id,
  status: "active", has_unread: false, created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
}));
function message(id: string, session: string, content: string): ChatMessage {
  return { id, chat_session_id: session, role: "user", content, task_id: null, created_at: "2026-11-15T00:00:00Z" };
}
function setup(term: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return renderHook(() => useChatMessageSearch("ws", sessions, term, true), { wrapper });
}

beforeEach(() => vi.mocked(api.listChatMessagesPage).mockReset());

describe("paged conversation search", () => {
  it("searches older pages before the next conversation and retains exact message destinations", async () => {
    const cursor = { id: "m1", created_at: "2026-11-15T00:00:00Z" };
    vi.mocked(api.listChatMessagesPage)
      .mockResolvedValueOnce({ messages: [message("m1", "one", "Nothing here")], has_more: true, next_cursor: cursor, limit: 100 })
      .mockResolvedValueOnce({ messages: [message("m2", "one", "Find the NEEDLE here")], has_more: false, limit: 100 })
      .mockResolvedValueOnce({ messages: [message("m3", "two", "Another needle")], has_more: false, limit: 100 });
    const { result } = setup("needle");
    await waitFor(() => expect(result.current.scanned).toBe(1));
    expect(result.current.hasNextPage).toBe(true);
    expect(result.current.matches).toEqual([]);

    await act(async () => { await result.current.fetchNextPage(); });
    await waitFor(() => expect(result.current.matches.map((match) => [match.session.id, match.messageId])).toEqual([["one", "m2"]]));
    await act(async () => { await result.current.fetchNextPage(); });

    await waitFor(() => expect(result.current.matches.map((match) => [match.session.id, match.messageId])).toEqual([["one", "m2"], ["two", "m3"]]));
    expect(result.current.scanned).toBe(3);
    expect(result.current.hasNextPage).toBe(false);
    expect(api.listChatMessagesPage).toHaveBeenNthCalledWith(2, "one", { before: cursor, limit: 100 });
  });

  it("keeps partial results and a retryable error when the next history page fails", async () => {
    vi.mocked(api.listChatMessagesPage)
      .mockResolvedValueOnce({ messages: [message("m1", "one", "Needle")], has_more: true, next_cursor: { id: "m1", created_at: "2026-11-15T00:00:00Z" }, limit: 100 })
      .mockRejectedValueOnce(new TypeError("Network unavailable"));
    const { result } = setup("needle");
    await waitFor(() => expect(result.current.matches).toHaveLength(1));

    await act(async () => { await result.current.fetchNextPage(); });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.matches[0]?.messageId).toBe("m1");
    expect(result.current.scanned).toBe(1);
  });
});
