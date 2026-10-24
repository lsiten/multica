"use client";

import { useEffect, useRef, useState } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { ArrowLeft, ChevronDown, ChevronUp, MessageSquare, PanelLeft, Users } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { useIsCompact } from "@multica/ui/hooks/use-mobile";
import { useRequiredWorkspaceSlug, useWorkspacePaths } from "@multica/core/paths";
import { getCurrentSlug } from "@multica/core/platform";
import { useChatStore, useDocModeStore, selectActiveDocument } from "@multica/core/chat";
import { chatQuickActionsPendingOptions } from "@multica/core/chat/queries";
import { useRegenerateChatQuickActions } from "@multica/core/chat/mutations";
import { useQuickActionsPendingTimeout } from "@multica/core/chat/use-quick-actions-pending-timeout";
import { useQuickActionsFailureToast } from "./components/use-quick-actions-failure-toast";
import { useQuery } from "@tanstack/react-query";
import type { Agent, ChatAutonomyPolicyOverride, ChatSession } from "@multica/core/types";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { ChatMessageList, ChatMessageSkeleton } from "./components/chat-message-list";
import { ChatInput, type ChatReference } from "./components/chat-input";
import { ChatQueue } from "./components/chat-queue";
import { ChatThreadList } from "./components/chat-thread-list";
import { ChatSessionHeader } from "./components/chat-session-header";
import { EmptyState } from "./components/chat-empty-state";
import { NewChatButton, AgentPicker } from "./components/new-chat-button";
import { useChatController } from "./components/use-chat-controller";
import { OfflineBanner } from "./components/offline-banner";
import { NoAgentBanner } from "./components/no-agent-banner";
import { ArchivedAgentBanner } from "./components/archived-agent-banner";
import { AgentAccessRevokedBanner } from "./components/agent-access-revoked-banner";
import { RuntimeRequiredBanner } from "./components/runtime-required-banner";
import { DocModeSurface } from "./components/doc-mode-surface";
import { DocModeFileList } from "./components/doc-file-list";
import { useDocWorkingIndicator } from "./components/use-doc-working-indicator";

/**
 * Chat tab — the first-class two-pane surface (thread list on the left,
 * conversation on the right), mirroring the Inbox page layout. Shares all
 * conversation logic with the floating FAB via `useChatController`; the
 * left rail reuses `ChatThreadList`.
 *
 * Selection is URL-addressable via `?session=<id>` so a thread can be
 * deep-linked, opened from a notification, and survive refresh. The chat
 * store's `activeSessionId` stays the source of truth (both surfaces read
 * it); the URL is kept in sync in both directions. `?agent=<id>` is the
 * complementary one-shot deep link for a NEW chat: it starts a fresh compose
 * bound to that agent and is then stripped from the URL.
 *
 * Starting a chat is where the agent is chosen: the header ⊕ opens an agent
 * picker (see NewChatButton), so the compose box no longer needs its own
 * agent selector. Unlike the FAB, this page passes no `contextItems` to
 * `ChatInput`, so its `@` mentions fall back to manual search (issue-comment
 * style).
 */
export function ChatPage() {
  const { t } = useT("chat");
  const { pathname, searchParams, replace } = useNavigation();
  const workspaceSlug = useRequiredWorkspaceSlug();
  const wsPaths = useWorkspacePaths();
  // App Router can retain this page after navigation. Once the URL belongs
  // to another route, this instance must stop reconciling shared chat state.
  // Also check the live workspace mirror in each effect: the incoming layout
  // can rehydrate the store before this page observes the destination URL.
  const isCurrentChatRoute = pathname === wsPaths.chat();
  const isCompact = useIsCompact();

  const c = useChatController({
    isActive: isCurrentChatRoute && getCurrentSlug() === workspaceSlug,
  });
  // Doc mode (v12): a document opened from the sidebar fills the detail pane
  // while the conversation stays put. Read here so it can gate the pane below.
  const activeDoc = useDocModeStore(selectActiveDocument(c.activeSessionId ?? ""));
  // The store can briefly retain an id while the session list is refreshing;
  // a document panel must follow the resolved session, not that stale pointer.
  const selectedDoc = c.currentSession ? activeDoc : null;
  // D7: floating "working" indicator bridges the agent-working signal
  // with doc mode (see useDocWorkingIndicator). Clearing it never closes doc mode.
  useDocWorkingIndicator(c.activeSessionId ?? "", !!c.pendingTaskId);
  const { data: quickActionsPending = null } = useQuery(
    chatQuickActionsPendingOptions(c.activeSessionId ?? ""),
  );
  // Drop a stuck pending marker (dead daemon / failed supplement) so the pill
  // spinner stops and a later refresh starts clean (MUL-5149).
  useQuickActionsPendingTimeout(c.activeSessionId ?? null, quickActionsPending);
  // Toast when an accepted refresh later fails in the daemon (async half).
  useQuickActionsFailureToast(c.activeSessionId ?? null);
  const regenerateQuickActions = useRegenerateChatQuickActions();
  const urlSession = searchParams.get("session") || null;
  const urlAgent = searchParams.get("agent") || null;

  // "Composing a brand-new chat" — the user hit ⊕ but hasn't sent yet, so no
  // session exists. At compact widths this decides list-vs-conversation; on desktop the
  // conversation pane is always mounted so it only needs to reset itself once a
  // real session takes over.
  const [composingNew, setComposingNew] = useState(false);
  // Collapse the file manager by default. Opening a chat must not fetch its
  // project directory, attachments, or issues until the user expands the
  // panel; the header toggle opens it on demand, and each tab loads only
  // its own content once it is selected.
  const [docFilesOpen, setDocFilesOpen] = useState(false);
  const [docComposerCollapsed, setDocComposerCollapsed] = useState(false);
  const [docReferences, setDocReferences] = useState<Record<string, ChatReference[]>>({});
  const [docQuoteFocusRequest, setDocQuoteFocusRequest] = useState(0);
  const [autonomyPolicy, setAutonomyPolicy] = useState<ChatAutonomyPolicyOverride | null>(null);
  useEffect(() => {
    setAutonomyPolicy(null);
  }, [c.activeSessionId, c.activeAgent?.id]);
  useEffect(() => {
    // A newly opened document starts with the composer available. Keep the
    // collapsed state local to the document surface so it never hides the
    // input after switching files or leaving/re-entering doc mode.
    setDocComposerCollapsed(false);
  }, [activeDoc?.id]);
  useEffect(() => {
    // Migrate the pre-Codex interaction's raw Markdown quote into the
    // removable reference chip once per session. This keeps drafts created by
    // older desktop builds from reappearing as a misplaced caret/large block.
    const sessionId = c.activeSessionId;
    if (!sessionId || (docReferences[sessionId]?.length ?? 0) > 0) return;
    const draft = useChatStore.getState().inputDrafts[sessionId] ?? "";
    const match = draft.match(/^> 引用文件：([^\n]+)\n\n([\s\S]+)$/);
    if (!match) return;
    const rawLabel = match[1];
    const rawContent = match[2];
    if (!rawLabel || !rawContent) return;
    const reference: ChatReference = {
      id: `${sessionId}:legacy:${rawLabel}:${rawContent.length}`,
      label: rawLabel.trim(),
      content: draft,
    };
    setDocReferences((current) => ({ ...current, [sessionId]: [reference] }));
    useChatStore.getState().clearInputDraft(sessionId);
  }, [c.activeSessionId, docReferences]);
  useEffect(() => {
    // Read the LIVE store value for the same reason as the session sync
    // effects below: under StrictMode's double-invoke this effect replays
    // with the render-captured snapshot, and a stale non-null session (a
    // persisted chat the URL→store effect already cleared) would revert the
    // composingNew=true that the later `?agent=` intent effect just set.
    if (useChatStore.getState().activeSessionId) setComposingNew(false);
  }, [c.activeSessionId]);

  // Two-way sync between the URL (`?session=`) and the chat store's
  // activeSessionId. Both effects read the LIVE store value via
  // `useChatStore.getState()` rather than the render-captured `c.activeSessionId`.
  // That is what keeps them from fighting on mount: a naive mirror effect fires
  // with the stale (null) snapshot and "corrects" the URL by stripping the
  // session before the URL→store effect has applied — breaking deep links and
  // making selection / new-chat feel unresponsive. Reading getState() sees the
  // value the sibling effect just wrote, so the reconciliation converges in one
  // pass and is idempotent under StrictMode's double-invoke.

  // URL → store: deep link, refresh, notification click, back/forward.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    if (urlSession !== useChatStore.getState().activeSessionId) {
      c.setActiveSession(urlSession);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reconcile URL/route changes, not store updates
  }, [isCurrentChatRoute, workspaceSlug, urlSession]);

  // store → URL: thread selection, "new chat", and sessions created by sending.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    const live = useChatStore.getState().activeSessionId;
    const current = searchParams.get("session") || null;
    if (live !== current) {
      const base = wsPaths.chat();
      replace(live ? `${base}?session=${live}` : base);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reconcile store/route changes, not URL updates
  }, [isCurrentChatRoute, workspaceSlug, c.activeSessionId]);

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_chat_layout",
  });

  // `?agent=` intent bookkeeping. The ref holds the param value already
  // consumed (or superseded) so the effect below fires at most once per deep
  // link — it also bridges the async window between replace() and the
  // searchParams actually dropping the param. Any explicit user action must
  // supersede a still-pending intent: agent/member queries can resolve late,
  // and a deferred intent firing after the user picked a thread (or started
  // another chat) would clobber that choice.
  const consumedAgentIntent = useRef<string | null>(null);
  const supersedeAgentIntent = () => {
    if (urlAgent) consumedAgentIntent.current = urlAgent;
  };

  const handleSelect = (session: ChatSession) => {
    supersedeAgentIntent();
    c.handleSelectSession(session);
    setComposingNew(false);
  };

  // Single archive path for both entry points (thread-list row + conversation
  // header). When the archived chat is the one in view, move the pane off it:
  // on desktop advance to the next chat (Inbox-style); when compact drop back to
  // the list, which reads more naturally than being thrown into an unrelated
  // conversation full-screen. Archiving any other chat leaves the view put.
  const handleArchive = (session: ChatSession) => {
    supersedeAgentIntent();
    if (session.id === c.activeSessionId) {
      if (isCompact) {
        c.setActiveSession(null);
        setComposingNew(false);
      } else {
        c.advanceSelectionAfterArchive(session);
      }
    }
    c.archiveSession(session.id);
  };

  const startNewChat = (agent: Agent | null) => {
    // A manual ⊕ pick outranks a pending deep link; when called FROM the
    // intent effect the ref is already set to this param, so this is a no-op.
    supersedeAgentIntent();
    if (agent) c.handleStartNewChat(agent);
    else c.handleNewChat();
    setComposingNew(true);
  };

  const startNewGroupChat = (agents: Agent[], title?: string) => {
    const lead = agents[0];
    if (lead) c.handleStartNewGroupChat(lead, agents, title);
  };

  const changeProjectContext = (projectId: string | null) => {
    if (projectId === c.activeProjectId) return;
    c.handleProjectChange(projectId);
    // Removing a project stays in the current conversation. Choosing a
    // project for an existing conversation starts a clean session, and a
    // compact layout must stay in the compose pane after activeSessionId is
    // cleared.
    if (!c.currentSession || projectId !== null) setComposingNew(true);
  };

  // Keep the mode selection on the chat page controlled just like the floating
  // composer. The per-message override is cleared after an accepted send so a
  // later ordinary message explicitly resets to normal mode instead of
  // accidentally inheriting the previous turn's limits.
  const handleSend = async (...args: Parameters<typeof c.handleSend>) => {
    const accepted = await c.handleSend(...args);
    if (accepted && args[4] !== undefined) setAutonomyPolicy(null);
    return accepted;
  };

  const handleAddToChat = (content: string) => {
    const sessionId = useChatStore.getState().activeSessionId;
    if (!sessionId) return;
    const label = content.match(/^> 引用文件：([^\n]+)/m)?.[1]?.trim() || "文档引用";
    const reference: ChatReference = {
      id: `${sessionId}:${label}:${content.length}:${Date.now()}`,
      label,
      content,
    };
    setDocReferences((current) => ({
      ...current,
      [sessionId]: [...(current[sessionId] ?? []), reference],
    }));
    setDocQuoteFocusRequest((request) => request + 1);
  };

  const activeReferences = c.activeSessionId ? docReferences[c.activeSessionId] ?? [] : [];
  const removeReference = (id: string) => {
    if (!c.activeSessionId) return;
    setDocReferences((current) => ({
      ...current,
      [c.activeSessionId!]: (current[c.activeSessionId!] ?? []).filter((item) => item.id !== id),
    }));
  };
  const consumeReferences = () => {
    if (!c.activeSessionId) return;
    setDocReferences((current) => ({ ...current, [c.activeSessionId!]: [] }));
  };

  // URL → new chat: `?agent=<id>` is the deep link used by "DM" entry points
  // (e.g. the agent detail page) to land on a fresh compose bound to that
  // agent. The permission-filtered agent list loads async, so the intent is
  // consumed on the render where the agent resolves, then the param is
  // stripped so refresh / the session sync above don't replay it. The ref
  // resets once the param is gone so a later identical deep link fires again.
  // A settled miss (access revoked, agent archived, bad id) is a denial: it
  // explains itself with a toast and consumes the intent so a later refetch
  // that surfaces the agent cannot start a chat without a fresh click. While
  // the queries are still loading the intent simply stays pending.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    if (!urlAgent) {
      consumedAgentIntent.current = null;
      return;
    }
    if (consumedAgentIntent.current === urlAgent) return;
    const agent = c.availableAgents.find((a) => a.id === urlAgent);
    if (agent) {
      consumedAgentIntent.current = urlAgent;
      startNewChat(agent);
      replace(wsPaths.chat());
      return;
    }
    if (c.agentsSettled) {
      consumedAgentIntent.current = urlAgent;
      toast.error(t(($) => $.page.agent_link_no_access));
      replace(wsPaths.chat());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- consume when the URL param or the resolving agent list changes
  }, [isCurrentChatRoute, workspaceSlug, urlAgent, c.availableAgents, c.agentsSettled]);

  const newChatButton = (
    <NewChatButton
      agents={c.availableAgents}
      userId={c.user?.id}
      onStart={startNewChat}
      side="bottom"
    />
  );

  const newGroupChatButton = (
    <AgentPicker
      agents={c.availableAgents}
      userId={c.user?.id}
      onSelect={() => undefined}
      onSelectMany={startNewGroupChat}
      multiSelect
      side="bottom"
      align="start"
      triggerRender={
        <Button
          variant="ghost"
          size="icon-sm"
          className="rounded-full text-muted-foreground"
          aria-label={t(($) => $.window.new_group_chat_tooltip)}
        />
      }
      trigger={<Users />}
    />
  );

  const listHeader = (
    <PageHeader>
      <h1 className="flex-1 text-body font-semibold">{t(($) => $.page.title)}</h1>
      <Button
        variant={docFilesOpen ? "secondary" : "ghost"}
        size="icon"
        onClick={() => setDocFilesOpen((open) => !open)}
        aria-label={t(($) => $.doc_file_list.toggle)}
        aria-pressed={docFilesOpen}
        className="text-muted-foreground"
      >
        <PanelLeft />
      </Button>
      {newChatButton}
      {newGroupChatButton}
    </PageHeader>
  );

  const listBody = (
    <div className="px-2 py-1">
      <ChatThreadList
        sessions={c.sessions}
        agents={c.agents}
        sessionsLoading={c.sessionsLoading}
        sessionsError={c.sessionsError}
        onRetrySessions={c.retrySessions}
        activeSessionId={c.activeSessionId}
        onSelectSession={handleSelect}
        onArchive={handleArchive}
      />
    </div>
  );

  // The conversation pane: message list / skeleton / empty above a persistent
  // banner + input. Identical composition to the floating window's body, so a
  // brand-new chat (no active session) shows the agent-aware empty state + input.
  // No compose-box agent selector — the agent is fixed when the chat starts.
  // `@container`: the conversation column's gutter (CHAT_GUTTER) widens with
  // THIS pane, which the user resizes independently of the browser window.
  const queuedTasks = c.pendingTask?.queued_tasks ?? [];
  const chatInput = (
    <ChatInput
      historyMessages={c.messages}
      historyWorkspaceId={c.wsId}
      onSend={handleSend}
      restoreDraftRequest={c.restoreDraftRequest}
      conversationStarterRequest={c.conversationStarterRequest}
      onConversationStarterApplied={c.handleConversationStarterApplied}
      onRestoreDraftApplied={c.handleRestoreDraftApplied}
      uploadEnabled={c.uploadEnabled && !c.isAgentAccessRevoked}
      onStop={c.handleStop}
      isRunning={!!c.pendingTaskId}
      allowSubmitWhileRunning={c.pendingTask?.supports_queue === true}
      disabled={
        c.isSessionArchived ||
        c.isAgentArchived ||
        c.isAgentAccessRevoked ||
        !c.isAgentRuntimeBound
      }
      noAgent={c.noAgent}
      agentArchived={c.isAgentArchived}
      agentAccessRevoked={c.isAgentAccessRevoked}
      agentRuntimeRequired={!c.isAgentRuntimeBound}
      agentName={c.activeAgent?.name}
      agentId={c.activeAgent?.id}
      projects={c.projects}
      projectId={c.activeProjectId}
      projectContextUnsupported={c.projectContextUnsupported}
      onProjectChange={changeProjectContext}
      squads={c.squads}
      squadId={c.activeSquadId}
      onSquadChange={c.handleSquadChange}
      workspaceScopeAllowed={c.workspaceScopeAllowed}
      isProjectUpdating={c.isProjectUpdating}
      focusRequest={c.focusInputRequest + docQuoteFocusRequest}
      autonomyPolicy={autonomyPolicy}
      onAutonomyPolicyChange={setAutonomyPolicy}
      embedded={!!selectedDoc}
        references={activeReferences}
        onRemoveReference={removeReference}
        onReferencesConsumed={consumeReferences}
    />
  );

  const docComposer = selectedDoc ? (
    <div className="pointer-events-none absolute inset-x-0 bottom-3 z-20 flex justify-center px-3 sm:px-5">
      {docComposerCollapsed ? (
        <Button
          variant="secondary"
          size="sm"
          className="pointer-events-auto gap-2 rounded-full border border-surface-border bg-surface-raised px-3 shadow-lg"
          onClick={() => setDocComposerCollapsed(false)}
          aria-label={t(($) => $.doc_mode.expand_chat)}
          title={t(($) => $.doc_mode.expand_chat)}
        >
          <span className="text-caption">{t(($) => $.doc_mode.chat_input)}</span>
          <ChevronUp className="h-4 w-4" />
        </Button>
      ) : (
        <div className="pointer-events-auto flex max-h-[min(72%,36rem)] w-full max-w-2xl min-w-0 flex-col overflow-hidden rounded-xl border border-surface-border bg-surface-raised/95 shadow-xl backdrop-blur-sm">
          <div className="flex h-9 shrink-0 items-center justify-between border-b bg-surface/60 px-3 text-caption text-muted-foreground">
            <span>{t(($) => $.doc_mode.chat_input)}</span>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground"
              onClick={() => setDocComposerCollapsed(true)}
              aria-label={t(($) => $.doc_mode.collapse_chat)}
              title={t(($) => $.doc_mode.collapse_chat)}
            >
              <ChevronDown className="h-4 w-4" />
            </Button>
          </div>
          <div className="min-h-0 overflow-hidden">{chatInput}</div>
        </div>
      )}
    </div>
  ) : null;

  const conversation = (
    <div className="flex flex-1 flex-col min-h-0 @container">
      {c.currentSession && (
        <ChatSessionHeader
          session={c.currentSession}
          agent={c.activeAgent}
          agents={c.agents}
          onArchive={handleArchive}
        />
      )}
      {c.messagesError ? (
        <div role="alert" className="flex flex-1 flex-col items-center justify-center gap-3 px-6 text-center text-muted-foreground">
          <p className="text-body">{t(($) => $.message_list.failure.fallback)}</p>
          <Button variant="outline" size="sm" onClick={c.retryMessages}>
            {t(($) => $.message_list.failure.retry)}
          </Button>
        </div>
      ) : c.showSkeleton ? (
        <ChatMessageSkeleton />
      ) : c.hasMessages ? (
        <ChatMessageList
          key={c.activeSessionId}
          messages={c.messages}
          pendingTask={c.pendingTask}
          availability={c.availability}
          firstItemIndex={c.firstItemIndex}
          hasOlderMessages={c.hasOlderMessages}
          isFetchingOlderMessages={c.isFetchingOlderMessages}
          onLoadOlderMessages={() => void c.fetchOlderMessages()}
          onQuickAction={(action) => c.handleSend(action.prompt)}
          quickActionsDisabled={
            !!c.pendingTaskId ||
            c.isSessionArchived ||
            c.isAgentArchived ||
            c.isAgentAccessRevoked ||
            !c.isAgentRuntimeBound ||
            c.noAgent
          }
          onRegenerateQuickActions={(message) =>
            c.activeSessionId
              ? regenerateQuickActions.mutateAsync({
                  sessionId: c.activeSessionId,
                  messageId: message.id,
                })
              : undefined
          }
          quickActionsPendingMessageId={quickActionsPending?.message_id ?? null}
        />
      ) : (
        <EmptyState
          agent={c.activeAgent}
          hasSessions={c.sessions.length > 0}
          onPickPrompt={c.prefillConversationStarter}
          customizeHref={c.customizeConversationStartersHref}
        />
      )}

      {c.isAgentAccessRevoked ? (
        <AgentAccessRevokedBanner agentName={c.activeAgent?.name} />
      ) : c.noAgent ? (
        <NoAgentBanner />
      ) : c.isAgentArchived ? (
        <ArchivedAgentBanner agentName={c.activeAgent?.name} />
      ) : !c.isAgentRuntimeBound && c.activeAgent ? (
        <RuntimeRequiredBanner
          agentId={c.activeAgent.id}
          agentName={c.activeAgent.name}
        />
      ) : (
        <OfflineBanner agentName={c.activeAgent?.name} availability={c.availability} />
      )}

      <ChatQueue
        tasks={queuedTasks}
        headStatus={c.pendingTask?.status}
        onSendNow={c.handleSendQueuedTaskNow}
        sendNowDisabled={c.isAgentAccessRevoked}
        onEdit={c.handleEditQueuedTask}
        onRemove={c.handleRemoveQueuedTask}
        onClear={c.handleClearQueuedTasks}
      />

      {chatInput}
    </div>
  );

  // -- Compact: list / conversation toggle -----------------------------------
  if (isCompact) {
    if (c.activeSessionId || composingNew) {
      return (
        <div className="flex flex-1 flex-col min-h-0">
          <div className="flex h-12 shrink-0 items-center border-b px-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                c.setActiveSession(null);
                setComposingNew(false);
              }}
              className="gap-1.5 text-muted-foreground"
            >
              <ArrowLeft className="h-4 w-4" />
              {t(($) => $.page.title)}
            </Button>
            <Button
              variant={docFilesOpen ? "secondary" : "ghost"}
              size="icon"
              onClick={() => setDocFilesOpen((open) => !open)}
              aria-label={t(($) => $.doc_file_list.toggle)}
              aria-pressed={docFilesOpen}
              className="ml-auto text-muted-foreground"
            >
              <PanelLeft />
            </Button>
          </div>
          {docFilesOpen && !!c.currentSession && (
            <div className="h-1/3 min-h-0 shrink-0 border-b overflow-hidden">
              <DocModeFileList wsId={c.wsId} sessionId={c.activeSessionId} session={c.currentSession} agents={c.agents} />
            </div>
          )}
          {selectedDoc ? (
            <div className="relative flex-1 min-h-0">
              <DocModeSurface wsId={c.wsId} sessionId={c.activeSessionId ?? ""} onAddToChat={handleAddToChat} />
              {docComposer}
            </div>
          ) : conversation}
        </div>
      );
    }
    return (
      <div className="flex flex-1 flex-col min-h-0">
        {listHeader}
        <div className="flex-1 min-h-0 overflow-y-auto">
          {listBody}
        </div>
      </div>
    );
  }

  // -- Desktop: resizable two-panel. The conversation pane appears only once
  // there is a chat target — an open thread or a new chat whose agent was just
  // picked via ⊕. With nothing selected there is no agent, so we show a neutral
  // prompt instead of an orphaned compose box. -------------------------------
  const hasTarget = !!c.activeSessionId || composingNew;
  return (
    <ResizablePanelGroup
      orientation="horizontal"
      className="flex-1 min-h-0"
      defaultLayout={defaultLayout}
      onLayoutChanged={onLayoutChanged}
    >
      <ResizablePanel
        id="list"
        defaultSize={260}
        minSize={240}
        maxSize={480}
        groupResizeBehavior="preserve-pixel-size"
      >
        <div className="flex flex-col border-r h-full">
          {listHeader}
          <div className="flex-1 min-h-0 overflow-y-auto">
            {listBody}
          </div>
        </div>
      </ResizablePanel>
      <ResizableHandle />
      <ResizablePanel id="detail" minSize="40%">
        <div className="flex flex-col min-h-0 h-full">
          {selectedDoc ? (
            <div className="relative flex-1 min-h-0">
              <DocModeSurface wsId={c.wsId} sessionId={c.activeSessionId ?? ""} onAddToChat={handleAddToChat} />
              {docComposer}
            </div>
          ) : hasTarget ? (
            conversation
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
              <MessageSquare className="h-10 w-10 text-faint-foreground" />
              <p className="text-body">{t(($) => $.page.select_prompt)}</p>
            </div>
          )}
        </div>
      </ResizablePanel>
      {docFilesOpen && !!c.currentSession && (
        <>
          <ResizableHandle />
          <ResizablePanel id="files" defaultSize={300} minSize={240} maxSize={480}>
            <div className="flex h-full min-h-0 flex-col border-l">
              <DocModeFileList wsId={c.wsId} sessionId={c.activeSessionId} session={c.currentSession} agents={c.agents} />
            </div>
          </ResizablePanel>
        </>
      )}
    </ResizablePanelGroup>
  );
}
