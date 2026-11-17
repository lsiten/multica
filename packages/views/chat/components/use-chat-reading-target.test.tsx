// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, type InfiniteData } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { chatKeys } from "@multica/core/chat/queries";
import type { ChatMessage, ChatMessagesPage } from "@multica/core/types";
import { useChatReadingTarget } from "./use-chat-reading-target";
import type { ChatReadingRow } from "./chat-reading-position";
import type { ChatSearchMatch } from "./use-chat-search-match";
import { readingScroller } from "./chat-reading-test-fixture";
import { useChatReadingPositionStore } from "@multica/core/chat";

vi.mock("@multica/core/api", async (original) => ({ ...await original<typeof import("@multica/core/api")>(), api: { listChatMessagesPage: vi.fn() } }));
beforeEach(() => {
  vi.mocked(api.listChatMessagesPage).mockReset();
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 0));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
});
afterEach(() => { vi.unstubAllGlobals(); document.body.replaceChildren(); });
const context = { workspaceId: "workspace", sessionId: "session" };
const initial = { kind: "message", messageId: "saved", offset: 180, createdAt: "2026-11-16T01:00:00Z" } as const;
const latest: ChatReadingRow = { messageId: "latest", createdAt: "2026-11-16T03:00:00Z", index: 0 };
const saved: ChatReadingRow = { messageId: "saved", createdAt: initial.createdAt, index: 0 };
function message(row: ChatReadingRow): ChatMessage { return { id: row.messageId, chat_session_id: "session", role: "assistant", content: "Stored answer", task_id: null, created_at: row.createdAt }; }
function setup(config: { rows?: readonly ChatReadingRow[]; page?: ChatMessagesPage; deferFirstScroll?: boolean } = {}) {
  const scroller = readingScroller({ deferFirstScroll: config.deferFirstScroll });
  const client = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
  client.setQueryData<InfiniteData<ChatMessagesPage>>(chatKeys.messagesPage("session"), { pages: [config.page ?? { messages: [message(latest)], has_more: true, next_cursor: { id: "latest", created_at: latest.createdAt }, limit: 50 }], pageParams: [null] });
  useChatReadingPositionStore.getState().setPosition(JSON.stringify([context.workspaceId, context.sessionId]), initial);
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const pin = vi.fn();
  const disengage = vi.fn();
  const initialProps: { rows: readonly ChatReadingRow[]; navigation?: ChatSearchMatch } = { rows: config.rows ?? [latest] };
  const hook = renderHook(({ rows, navigation }: typeof initialProps) => useChatReadingTarget({ context, initial, rows, container: scroller.element, list: scroller.list, active: true, explicitNavigation: navigation, disengage, pin }), { initialProps, wrapper });
  return { ...hook, ...scroller, client, pin };
}

it("loads the bookmarked older message and restores its intra-message reading offset", async () => {
  vi.mocked(api.listChatMessagesPage).mockResolvedValue({ messages: [message(saved)], has_more: false, limit: 50 });
  const view = setup();
  await waitFor(() => expect(view.client.getQueryData<InfiniteData<ChatMessagesPage>>(chatKeys.messagesPage("session"))?.pages).toHaveLength(2));

  view.rerender({ rows: [saved, { ...latest, index: 1 }] });

  await waitFor(() => expect(view.result.current.ready).toBe(true));
  expect(view.result.current.failed).toBe(false);
  expect(view.element.contains(view.anchor)).toBe(true);
  expect(view.element.getBoundingClientRect().top - view.anchor.getBoundingClientRect().top).toBe(180);
  expect(view.pin).not.toHaveBeenCalled();
  view.unmount();
});

it("does not restore an old bookmark after explicit source navigation interrupts its history load", async () => {
  let complete: (page: ChatMessagesPage) => void = () => { throw new Error("History read not started"); };
  vi.mocked(api.listChatMessagesPage).mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }));
  const view = setup();
  await waitFor(() => expect(api.listChatMessagesPage).toHaveBeenCalled());
  view.element.scrollTop = 125;

  view.rerender({ rows: [latest], navigation: { messageId: "new-source-not-loaded", term: "source" } });
  await act(async () => complete({ messages: [message(saved)], has_more: false, limit: 50 }));
  view.rerender({ rows: [saved, { ...latest, index: 1 }], navigation: { messageId: "new-source-not-loaded", term: "source" } });

  await waitFor(() => expect(view.result.current.ready).toBe(true));
  expect(view.element.scrollTop).toBe(125);
  expect(view.element.contains(view.anchor)).toBe(false);
  expect(view.pin).not.toHaveBeenCalled();
  view.unmount();
});

it("keeps a failed history restore retryable without replacing the original bookmark", async () => {
  vi.mocked(api.listChatMessagesPage).mockRejectedValue(new Error("History unavailable"));
  const view = setup();
  await waitFor(() => expect(view.result.current.failed).toBe(true));
  expect(useChatReadingPositionStore.getState().positions[JSON.stringify([context.workspaceId, context.sessionId])]).toEqual(initial);
  vi.mocked(api.listChatMessagesPage).mockResolvedValue({ messages: [message(saved)], has_more: false, limit: 50 });

  act(() => view.result.current.retry());
  await waitFor(() => expect(view.client.getQueryData<InfiniteData<ChatMessagesPage>>(chatKeys.messagesPage("session"))?.pages).toHaveLength(2));
  view.rerender({ rows: [saved, { ...latest, index: 1 }] });

  await waitFor(() => expect(view.result.current.ready).toBe(true));
  expect(view.result.current.failed).toBe(false);
  expect(view.element.getBoundingClientRect().top - view.anchor.getBoundingClientRect().top).toBe(180);
  view.unmount();
});

it("returns to the nearest available message when the bookmarked message was deleted", async () => {
  const nearby: ChatReadingRow = { messageId: "nearby", createdAt: "2026-11-16T00:59:00Z", index: 0 };
  const view = setup({ rows: [nearby, { ...latest, index: 1 }], page: { messages: [message(latest), message(nearby)], has_more: false, limit: 50 } });
  view.anchor.dataset.chatMessageId = "nearby";

  await waitFor(() => expect(view.result.current.ready).toBe(true));

  expect(view.result.current.failed).toBe(false);
  expect(view.element.contains(view.anchor)).toBe(true);
  expect(view.anchor.getBoundingClientRect().top).toBe(view.element.getBoundingClientRect().top);
  expect(view.pin).not.toHaveBeenCalled();
  view.unmount();
});

it("restores the bookmark when history prepend invalidates the first virtual scroll", async () => {
  const view = setup({ rows: [saved], page: { messages: [message(saved)], has_more: false, limit: 50 }, deferFirstScroll: true });

  await waitFor(() => expect(view.result.current.ready).toBe(true));

  expect(view.result.current.failed).toBe(false);
  expect(view.element.contains(view.anchor)).toBe(true);
  expect(view.element.getBoundingClientRect().top - view.anchor.getBoundingClientRect().top).toBe(180);
  view.unmount();
});
