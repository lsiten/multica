"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useDocModeStore, selectActiveDocument, documentDraftKey } from "@multica/core/chat";
import { chatQuickActionsPendingOptions } from "@multica/core/chat/queries";
import { useRegenerateChatQuickActions } from "@multica/core/chat/mutations";
import { useQuickActionsPendingTimeout } from "@multica/core/chat/use-quick-actions-pending-timeout";
import type { Agent, ChatSession } from "@multica/core/types";
import { ChatAppRequests } from "../../common/app-requests";
import { ChatComposer } from "./chat-composer";
import { ChatMessageList, ChatMessageSkeleton, type ChatMessageEnterTransition } from "./chat-message-list";
import { ChatWakeupStrip, useChatWakeups } from "./chat-wakeups";
import { ChatQueue } from "./chat-queue";
import { ChatSessionHeader } from "./chat-session-header";
import { ChatWorkbench } from "./chat-workbench";
import { ChatResourceTabs } from "./chat-resource-tabs";
import { ChatMessageSearch } from "./chat-message-search";
import { DocModeFileList } from "./doc-file-list";
import { DocModeSurface } from "./doc-mode-surface";
import { EmptyState } from "./chat-empty-state";
import { OfflineBanner } from "./offline-banner";
import { NoAgentBanner } from "./no-agent-banner";
import { ArchivedAgentBanner } from "./archived-agent-banner";
import { AgentAccessRevokedBanner } from "./agent-access-revoked-banner";
import { RuntimeRequiredBanner } from "./runtime-required-banner";
import { useChatAnnotations } from "./use-chat-annotations";
import { useChatRunControls } from "./use-chat-run-controls";
import { useChatReferences } from "./use-chat-references";
import { useChatMessageActions } from "./use-chat-message-actions";
import { useMessageSourceNavigation } from "./use-message-source-navigation";
import { useDocWorkingIndicator } from "./use-doc-working-indicator";
import { useQuickActionsFailureToast } from "./use-quick-actions-failure-toast";
import type { useChatController } from "./use-chat-controller";

export function ChatPageConversation({ controller: c, leading, onArchive, onProjectChange, onStartNewChat, sourceMessageId, enterTransition }: {
  readonly controller: ReturnType<typeof useChatController>;
  readonly leading?: ReactNode;
  readonly onArchive: (session: ChatSession) => void;
  readonly onProjectChange: (projectId: string | null) => void;
  readonly sourceMessageId?: string;
  readonly enterTransition?: ChatMessageEnterTransition | null;
  readonly onStartNewChat?: (agent: Agent) => void;
}) {
  const wakeups = useChatWakeups(c.activeSessionId);
  const controls = useChatRunControls(c);
  const messageActions = useChatMessageActions(c, onStartNewChat);
  const { data: quickActionsPending = null } = useQuery(chatQuickActionsPendingOptions(c.activeSessionId ?? ""));
  useQuickActionsPendingTimeout(c.activeSessionId, quickActionsPending);
  useQuickActionsFailureToast(c.activeSessionId);
  const regenerateQuickActions = useRegenerateChatQuickActions();
  const annotations = useChatAnnotations({
    sessionId: c.activeSessionId,
    agentName: c.activeAgent?.name,
    enabled: !c.isSessionArchived && !c.isAgentArchived && !c.isAgentAccessRevoked && c.isAgentRuntimeBound && !c.noAgent,
  });
  const [searchTarget, setSearchTarget] = useState<{ sessionId: string; messageId: string; term: string } | null>(null);
  const openMessageReference = useMessageSourceNavigation(c.wsId, (session, messageId) => {
    c.handleSelectSession(session);
    useDocModeStore.getState().selectChat(session.id);
    setSearchTarget({ sessionId: session.id, messageId, term: "" });
  });
  const references = useChatReferences(c.wsId, c.activeSessionId, c.requestInputFocus, openMessageReference);
  useEffect(() => {
    if (!sourceMessageId || !c.activeSessionId) return;
    useDocModeStore.getState().selectChat(c.activeSessionId);
    setSearchTarget({ sessionId: c.activeSessionId, messageId: sourceMessageId, term: "" });
  }, [sourceMessageId, c.activeSessionId]);
  const revealMessage = annotations.revealMessage;
  useEffect(() => {
    if (searchTarget?.sessionId === c.activeSessionId) void revealMessage(searchTarget.messageId);
  }, [c.activeSessionId, revealMessage, searchTarget]);
  const activeDoc = useDocModeStore(selectActiveDocument(c.activeSessionId));
  useDocWorkingIndicator(c.activeSessionId ?? "", c.pendingTask?.status === "running");
  const sessionKey = c.activeSessionId ?? "";
  const disabled = !!c.pendingTaskId || c.isSessionArchived || c.isAgentArchived ||
    c.isAgentAccessRevoked || !c.isAgentRuntimeBound || c.noAgent;

  const conversation = (
    <>
      <div data-chat-transcript className="flex min-h-0 flex-1 flex-col">
      {c.showSkeleton ? <ChatMessageSkeleton /> : c.hasMessages ? (
        <ChatMessageList
          enterTransition={enterTransition}
          revealKey={c.activeSessionId}
          wakeups={wakeups}
          readingContext={c.activeSessionId ? { workspaceId: c.wsId, sessionId: c.activeSessionId } : undefined}
          onQuoteMessage={messageActions.onQuoteMessage}
          onStartDiscussion={messageActions.onStartDiscussion}
          referenceWorkspaceId={c.wsId}
          onOpenMessageReference={openMessageReference}
          onOpenDocumentReference={(reference) => useDocModeStore.getState().openDoc(sessionKey, reference.resource)}
          searchMatch={searchTarget?.sessionId === c.activeSessionId ? searchTarget : undefined}
          key={c.activeSessionId}
          messages={c.messages}
          pendingTask={c.pendingTask}
          availability={c.availability}
          firstItemIndex={c.firstItemIndex}
          hasOlderMessages={c.hasOlderMessages}
          isFetchingOlderMessages={c.isFetchingOlderMessages}
          onLoadOlderMessages={() => void c.fetchOlderMessages()}
          onQuickAction={(action) => c.handleSend(action.prompt)}
          quickActionsDisabled={disabled}
          onRegenerateQuickActions={(message) => c.activeSessionId
            ? regenerateQuickActions.mutateAsync({ sessionId: c.activeSessionId, messageId: message.id })
            : undefined}
          quickActionsPendingMessageId={quickActionsPending?.message_id ?? null}
          annotations={annotations}
          onFailureAction={controls.handleFailureAction}
          failureActionsDisabled={disabled}
        />
      ) : (
        <EmptyState agent={c.activeAgent} hasSessions={c.sessions.length > 0} onPickPrompt={c.sendConversationStarter} />
      )}
      </div>
      {c.isAgentAccessRevoked ? <AgentAccessRevokedBanner agentName={c.activeAgent?.name} />
        : c.noAgent ? <NoAgentBanner />
        : c.isAgentArchived ? <ArchivedAgentBanner agentName={c.activeAgent?.name} />
        : !c.isAgentRuntimeBound && c.activeAgent
          ? <RuntimeRequiredBanner agentId={c.activeAgent.id} agentName={c.activeAgent.name} />
          : <OfflineBanner agentName={c.activeAgent?.name} availability={c.availability} />}
      <div className="px-4"><ChatAppRequests sessionId={c.activeSessionId} /></div>
      <ChatWakeupStrip sessionId={c.activeSessionId} wakeups={wakeups} />
      <ChatQueue
        wakeups={wakeups}
        tasks={c.pendingTask?.queued_tasks ?? []}
        headStatus={c.pendingTask?.status}
        onSendNow={c.handleSendQueuedTaskNow}
        sendNowDisabled={c.isAgentAccessRevoked}
        onEdit={c.handleEditQueuedTask}
        onRemove={c.handleRemoveQueuedTask}
        onClear={c.handleClearQueuedTasks}
      />
      <ChatComposer controller={c} controls={controls} annotations={annotations} inputProps={{
        ...references.inputProps,
        onProjectChange,
      }} />
    </>
  );

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col @container">
      {c.currentSession && <ChatSessionHeader session={c.currentSession} agent={c.activeAgent} onArchive={onArchive} leading={leading} />}
      <ChatWorkbench
        scope={JSON.stringify([c.wsId, c.activeSessionId])}
        documentKey={activeDoc ? documentDraftKey(activeDoc) : null}
        selectedDocument={activeDoc}
        conversationTitle={c.currentSession?.title}
        actions={<ChatMessageSearch wsId={c.wsId} sessions={c.sessions} activeSessionId={c.activeSessionId} onSelect={(session, messageId, term) => {
          c.handleSelectSession(session);
          useDocModeStore.getState().selectChat(session.id);
          setSearchTarget({ sessionId: session.id, messageId, term });
        }} />}
        tabs={c.activeSessionId ? <ChatResourceTabs scope={JSON.stringify([c.wsId, c.activeSessionId])} sessionId={c.activeSessionId} /> : null}
        conversation={conversation}
        document={activeDoc ? <DocModeSurface key={`${activeDoc.kind}:${activeDoc.id}`} wsId={c.wsId} sessionId={sessionKey} onAddToChat={references.addReference} /> : null}
        files={<DocModeFileList wsId={c.wsId} sessionId={c.activeSessionId} session={c.currentSession} agents={c.agents} />}
      />
      {messageActions.dialog}
    </div>
  );
}
