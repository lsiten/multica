// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createChatStore, registerChatStore, useChatStore } from "@multica/core/chat";
import { CHAT_SESSION_TAB_MIME, CHAT_SESSION_TAB_PROMOTE_EVENT } from "@multica/core/chat";
import { chatKeys, chatSessionsOptions } from "@multica/core/chat/queries";
import type { ChatSession } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

const memory = new Map<string, string>();
registerChatStore(
  createChatStore({
    storage: {
      getItem: (k) => memory.get(k) ?? null,
      setItem: (k, v) => void memory.set(k, v),
      removeItem: (k) => void memory.delete(k),
    },
  }),
);

// The header is what's under test, so the conversation body, composer and
// network layer are stubbed; requests stay pending forever.
const cancelRequest = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: new Proxy({}, { get: (_target, key) => key === "cancelTaskById" ? cancelRequest : () => new Promise(() => {}) }),
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/hooks")>()),
  useWorkspaceId: () => "ws-1",
}));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspaceSlug: () => "acme",
}));
vi.mock("@multica/core/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/agents")>()),
  useCustomizeConversationStartersHref:()=>null, useAgentPresenceDetail: () => "loading",
  useWorkspaceAgentAvailability: () => "loading",
}));
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useIsCompact: () => false,
}));
const resize = vi.hoisted(() => ({ isAtMax: false }));
vi.mock("./use-chat-resize", () => ({
  useChatResize: () => ({
    renderWidth: 380,
    renderHeight: 600,
    isAtMax: resize.isAtMax,
    boundsReady: true,
    isDragging: false,
    toggleExpand: () => {},
    startDrag: () => {},
  }),
}));
vi.mock("./use-chat-context-items", () => ({ useChatContextItems: () => [] }));
vi.mock("./chat-window-messages", () => ({ ChatWindowMessages: () => null }));
vi.mock("../../common/app-requests", () => ({ ChatAppRequests: () => null }));
const chatInputProps = vi.hoisted(() => ({ last: null as { hidden?: boolean } | null }));
vi.mock("./chat-input", () => ({
  ChatInput: (props: { hidden?: boolean }) => {
    chatInputProps.last = props;
    return null;
  },
}));
vi.mock("./chat-queue", () => ({ ChatQueue: () => null }));
vi.mock("./chat-empty-state", () => ({ EmptyState: () => null }));
vi.mock("./session-history-list", () => ({ SessionHistoryList: ({ sessions, renderRow }: { sessions: ChatSession[]; renderRow: (session: ChatSession) => ReactNode }) => sessions.map(renderRow) }));
vi.mock("./chat-resize-handles", () => ({ ChatResizeHandles: () => null }));
vi.mock("./offline-banner", () => ({ OfflineBanner: () => null }));
vi.mock("./no-agent-banner", () => ({ NoAgentBanner: () => null }));
vi.mock("./archived-agent-banner", () => ({ ArchivedAgentBanner: () => null }));
vi.mock("./agent-access-revoked-banner", () => ({ AgentAccessRevokedBanner: () => null }));
vi.mock("./runtime-required-banner", () => ({ RuntimeRequiredBanner: () => null }));

import { ChatWindow } from "./chat-window";

// With no session open the session picker's visible label is also "New chat";
// an open session keeps the header button the only control with that name.
const session: ChatSession = {
  id: "session-1",
  workspace_id: "ws-1",
  agent_id: "agent-1",
  creator_id: "user-1",
  title: "Roadmap",
  status: "active",
  has_unread: false,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
};

function renderWindow(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  qc.setQueryData(chatSessionsOptions("ws-1").queryKey, [session]);
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ChatWindow />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cancelRequest.mockReset();
  resize.isAtMax = false;
  act(() => {
    useChatStore.setState({ isOpen: true, isExpanded: false, activeSessionId: session.id });
  });
});

describe("stopping a run from chat history", () => {
  it("keeps task feedback until server confirmation and preserves the confirmation after failure", async () => {
    const pending = Promise.withResolvers<{ id: string; status: string }>();
    cancelRequest.mockReturnValue(pending.promise);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const aggregate = { tasks: [{ task_id: "task-1", status: "running", chat_session_id: session.id }] };
    const task = { task_id: "task-1", status: "running" };
    qc.setQueryData(chatKeys.pendingTasks("ws-1"), aggregate);
    qc.setQueryData(chatKeys.pendingTask(session.id), task);
    renderWindow(qc);

    fireEvent.click(screen.getByRole("button", { name: /Roadmap/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Stop current run" }));
    fireEvent.click(screen.getByRole("button", { name: "Stop run" }));
    await waitFor(() => expect(cancelRequest).toHaveBeenCalledExactlyOnceWith("task-1", undefined));
    expect(qc.getQueryData(chatKeys.pendingTasks("ws-1"))).toEqual(aggregate);
    expect(qc.getQueryData(chatKeys.pendingTask(session.id))).toEqual(task);

    await act(async () => { pending.reject(new Error("Network unavailable")); await pending.promise.catch(() => {}); });
    expect(await screen.findByRole("button", { name: "Stop run" })).toBeEnabled();
    expect(qc.getQueryData(chatKeys.pendingTask(session.id))).toEqual(task);
  });
});

describe("ChatWindow header buttons", () => {
  it("names the new chat, expand, minimize and close buttons after their tooltips", () => {
    renderWindow();

    expect(screen.getByRole("button", { name: enChat.window.new_chat_tooltip })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enChat.window.expand_tooltip })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enChat.window.minimize_tooltip })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enChat.window.close_tooltip })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: enChat.window.restore_tooltip }),
    ).not.toBeInTheDocument();
  });

  it("names the toggle Restore while the window is expanded", () => {
    act(() => {
      useChatStore.setState({ isExpanded: true });
    });
    renderWindow();

    expect(screen.getByRole("button", { name: enChat.window.restore_tooltip })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: enChat.window.expand_tooltip }),
    ).not.toBeInTheDocument();
  });

  it("names the toggle Restore when a manual resize already fills the bounds", () => {
    resize.isAtMax = true;
    renderWindow();

    expect(screen.getByRole("button", { name: enChat.window.restore_tooltip })).toBeInTheDocument();
  });

  it("exposes the active chat as a desktop-tab drag payload", () => {
    renderWindow();
    const dragHandle = document.querySelector<HTMLElement>("[draggable=true]");
    expect(dragHandle).not.toBeNull();
    const data = new Map<string, string>();
    fireEvent.dragStart(dragHandle!, {
      dataTransfer: {
        effectAllowed: "",
        setData: (type: string, value: string) => data.set(type, value),
      },
    });
    expect(data.get(CHAT_SESSION_TAB_MIME)).toBe(session.id);
    expect(data.get("text/plain")).toBe(session.title);
  });

  it("promotes when the pointer drag is released in the top tab strip", () => {
    const setPointerCapture = vi.fn();
    const releasePointerCapture = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "setPointerCapture", { configurable: true, value: setPointerCapture });
    Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", { configurable: true, value: releasePointerCapture });
    Object.defineProperty(HTMLElement.prototype, "hasPointerCapture", { configurable: true, value: () => false });
    const promote = vi.fn();
    window.addEventListener(CHAT_SESSION_TAB_PROMOTE_EVENT, promote);
    try {
      renderWindow();
      const dragHandle = document.querySelector<HTMLElement>("[draggable=true]");
      expect(dragHandle).not.toBeNull();
      fireEvent.pointerDown(dragHandle!, { button: 0, pointerId: 7, clientY: 360 });
      fireEvent.pointerUp(dragHandle!, { pointerId: 7, clientY: 40 });
      expect(promote).toHaveBeenCalledOnce();
      expect(promote.mock.calls[0]?.[0]).toMatchObject({
        type: CHAT_SESSION_TAB_PROMOTE_EVENT,
        detail: { sessionId: session.id, title: session.title },
      });
    } finally {
      window.removeEventListener(CHAT_SESSION_TAB_PROMOTE_EVENT, promote);
      delete (HTMLElement.prototype as Partial<HTMLElement>).setPointerCapture;
      delete (HTMLElement.prototype as Partial<HTMLElement>).releasePointerCapture;
      delete (HTMLElement.prototype as Partial<HTMLElement>).hasPointerCapture;
    }
  });
});

describe("ChatWindow minimize and close", () => {
  beforeEach(() => {
    act(() => {
      useChatStore.setState({ chatBarKept: [session.id], chatBarDismissed: {} });
    });
  });

  it("minimize hides the window and keeps the chat's chip", () => {
    renderWindow();

    fireEvent.click(screen.getByRole("button", { name: enChat.window.minimize_tooltip }));

    expect(useChatStore.getState().isOpen).toBe(false);
    expect(useChatStore.getState().chatBarKept).toEqual([session.id]);
  });

  it("close hides the window and closes the chat's chip", () => {
    renderWindow();

    fireEvent.click(screen.getByRole("button", { name: enChat.window.close_tooltip }));

    const state = useChatStore.getState();
    expect(state.isOpen).toBe(false);
    expect(state.chatBarKept).toEqual([]);
    expect(state.chatBarDismissed).toEqual({ [session.id]: session.updated_at });
  });

  it("turns off voice input in the composer while the window is closed", () => {
    renderWindow();
    expect(chatInputProps.last?.hidden).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: enChat.window.minimize_tooltip }));

    expect(chatInputProps.last?.hidden).toBe(true);
  });
});
