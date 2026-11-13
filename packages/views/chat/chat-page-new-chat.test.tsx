// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { createChatStore, registerChatStore, useChatStore } from "@multica/core/chat";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { setCurrentWorkspace } from "@multica/core/platform";
import type { Agent, ChatSession } from "@multica/core/types";
import { NavigationProvider } from "../navigation";
import enChat from "../locales/en/chat.json";
import enCommon from "../locales/en/common.json";
import enIssues from "../locales/en/issues.json";
import { ChatPage } from "./chat-page";

const layout = vi.hoisted(() => ({ compact: false, collapsed: false }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => layout.compact,
  useIsCompact: () => layout.compact,
}));
vi.mock("../layout/app-shell-context", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../layout/app-shell-context")>()),
  useListColumnCollapsed: () => layout.collapsed,
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
    { getState: () => ({ user: { id: "user-1" } }) },
  ),
}));
vi.mock("@multica/core/agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/agents")>()),
  useAgentPresenceDetail: () => ({ availability: "online" }),
  useCustomizeConversationStartersHref: () => null,
  useWorkspaceAgentAvailability: () => "available",
}));
vi.mock("../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./components/chat-input", () => ({ ChatInput: () => <div>chat-input</div> }));
vi.mock("./components/chat-empty-state", () => ({ EmptyState: () => null }));
vi.mock("./components/chat-thread-list", () => ({ ChatThreadList: () => <div>thread-list</div> }));
vi.mock("./components/chat-session-header", () => ({
  ChatSessionHeader: ({ session }: { session: ChatSession }) => <h2>{session.title}</h2>,
}));
vi.mock("./components/doc-file-list", () => ({ DocModeFileList: () => null }));
vi.mock("./components/chat-message-list", () => ({
  ChatMessageList: () => null,
  ChatMessageSkeleton: () => null,
}));
vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listAgents: vi.fn(),
      listMembers: vi.fn(async () => [{ user_id: "user-1", role: "admin" }]),
      listProjects: vi.fn(async () => []),
      listSquads: vi.fn(async () => []),
      listChatSessions: vi.fn(),
      createGroupChat: vi.fn(),
      createChatSession: vi.fn(),
      markChatSessionRead: vi.fn(async () => undefined),
      listChatMessagesPage: vi.fn(async () => ({ messages: [], has_more: false })),
      getPendingChatTask: vi.fn(async () => null),
      listChatDraftRestores: vi.fn(async () => ({ restores: [] })),
    },
  };
});

const alpha: Agent = {
  id: "alpha", workspace_id: "ws-1", runtime_id: "runtime-1", name: "Alpha",
  description: "", instructions: "", avatar_url: null, runtime_mode: "local",
  runtime_config: {}, custom_args: [], visibility: "workspace", permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }], status: "idle",
  max_concurrent_tasks: 1, model: "", owner_id: "user-1", skills: [],
  created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z",
  archived_at: null, archived_by: null,
};
const beta: Agent = { ...alpha, id: "beta", name: "Beta" };
const group: ChatSession = {
  id: "group-1", workspace_id: "ws-1", creator_id: "user-1", agent_id: beta.id,
  title: "Release review", status: "active", has_unread: false, unread_count: 0,
  last_message: null, pinned: false, created_at: alpha.created_at, updated_at: alpha.updated_at,
};
let queryClient: QueryClient;

beforeEach(() => {
  vi.clearAllMocks();
  layout.compact = false;
  layout.collapsed = false;
  localStorage.clear();
  setCurrentWorkspace("acme", "ws-1");
  registerChatStore(createChatStore({ storage: localStorage }));
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  vi.mocked(api.listAgents).mockResolvedValue([alpha, beta]);
  vi.mocked(api.listChatSessions).mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  queryClient.clear();
  setCurrentWorkspace(null, null);
});

function renderPage() {
  const replace = vi.fn();
  render(
    <QueryClientProvider client={queryClient}>
      <WorkspaceSlugProvider slug="acme">
        <I18nProvider locale="en" resources={{ en: { chat: enChat, common: enCommon, issues: enIssues } }}>
          <NavigationProvider value={{
            pathname: "/acme/chat", searchParams: new URLSearchParams(), hash: "",
            push: vi.fn(), replace, back: vi.fn(), getShareableUrl: (path) => path,
          }}>
            <ChatPage />
          </NavigationProvider>
        </I18nProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
  return { replace };
}

describe("ChatPage new conversation actions", () => {
  it("opens a direct compose for the selected agent without creating a session", async () => {
    renderPage();
    await waitFor(() => expect(api.listAgents).toHaveBeenCalled());
    expect(useChatStore.getState().selectedAgentId).toBeNull();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: enChat.window.new_chat_tooltip })).toHaveAttribute(
        "aria-haspopup",
        "dialog",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: enChat.window.new_chat_tooltip }));
    fireEvent.click(await screen.findByText(beta.name));

    expect(useChatStore.getState().selectedAgentId).toBe(beta.id);
    expect(screen.getByText("chat-input")).toBeInTheDocument();
    expect(api.createChatSession).not.toHaveBeenCalled();
    expect(api.createGroupChat).not.toHaveBeenCalled();
  });

  it("creates a group chat from the selected agents and title", async () => {
    vi.mocked(api.createGroupChat).mockResolvedValue(group);
    renderPage();
    await waitFor(() => expect(api.listAgents).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByRole("button", { name: enChat.window.new_group_chat_tooltip })).toHaveAttribute(
        "aria-haspopup",
        "dialog",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: enChat.window.new_group_chat_tooltip }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByText(alpha.name));
    fireEvent.click(within(dialog).getByText(beta.name));
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: enChat.window.group_title_placeholder }),
      { target: { value: "Release review" } },
    );
    fireEvent.click(within(dialog).getByRole("button", { name: /Create group \(2\)/ }));

    await waitFor(() => expect(api.createGroupChat).toHaveBeenCalledWith(expect.objectContaining({
      agent_id: alpha.id,
      title: "Release review",
      participants: [{ member_type: "agent", member_id: beta.id }],
    })));
  });
});
