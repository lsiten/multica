"use client";

import { useEffect, useRef, useState, type ComponentProps } from "react";
import { useChatTaskStopping } from "@multica/core/chat";
import type { ChatMessage } from "@multica/core/types";
import { useT } from "../../i18n";
import { ChatInput } from "./chat-input";
import type { ChatAnnotations } from "./use-chat-annotations";
import type { useChatController } from "./use-chat-controller";
import type { useChatRunControls } from "./use-chat-run-controls";

type InputOverrides = Pick<ComponentProps<typeof ChatInput>,
  "hidden" | "references" | "onRemoveReference" | "onOpenReference" | "onReferencesConsumed" |
  "leftAdornment" | "contextItems" | "pageContext" | "focusRequest" | "onProjectChange"
>;

export type RunReceipt = "completed" | "failed";

/** Derive a terminal receipt only from the persisted assistant outcome. */
export function deriveRunReceipt(taskId: string, messages: readonly Pick<ChatMessage, "task_id" | "role" | "failure_reason">[]): RunReceipt | null {
  const assistant = messages.find((message) => message.role === "assistant" && message.task_id === taskId);
  if (!assistant) return null;
  return assistant.failure_reason ? "failed" : "completed";
}

/** One composer contract for full-page and floating conversations. */
export function ChatComposer({ controller: c, controls, annotations, inputProps }: {
  readonly controller: ReturnType<typeof useChatController>;
  readonly controls: ReturnType<typeof useChatRunControls>;
  readonly annotations: ChatAnnotations;
  readonly inputProps?: InputOverrides;
}) {
  const { t } = useT("chat");
  const stopping = useChatTaskStopping(c.wsId, c.activeSessionId, c.pendingTaskId);
  const previousPendingTaskIdRef = useRef<string | null>(c.pendingTaskId);
  const awaitingReceiptTaskIdRef = useRef<string | null>(null);
  const [runReceipt, setRunReceipt] = useState<RunReceipt | null>(null);

  useEffect(() => {
    const previous = previousPendingTaskIdRef.current;
    if (previous && !c.pendingTaskId) awaitingReceiptTaskIdRef.current = previous;
    previousPendingTaskIdRef.current = c.pendingTaskId;

    const taskId = awaitingReceiptTaskIdRef.current;
    if (!taskId) return;
    const receipt = deriveRunReceipt(taskId, c.messages);
    if (!receipt) return;
    awaitingReceiptTaskIdRef.current = null;
    setRunReceipt(receipt);
  }, [c.messages, c.pendingTaskId]);

  useEffect(() => {
    if (!runReceipt) return;
    const timer = setTimeout(() => setRunReceipt(null), 2500);
    return () => clearTimeout(timer);
  }, [runReceipt]);

  return (
    <>
    {stopping && <p role="status" className="px-5 py-1 text-caption text-muted-foreground">{t(($) => $.workspace.stopping)}</p>}
    {runReceipt && (
      <p role="status" aria-live="polite" className={runReceipt === "failed" ? "px-5 py-1 text-caption text-destructive" : "px-5 py-1 text-caption text-muted-foreground"}>
        {runReceipt === "failed" ? t(($) => $.list.failed) : t(($) => $.session_history.row_subtitle.completed)}
      </p>
    )}
    <ChatInput
      historyMessages={c.messages}
      historyWorkspaceId={c.wsId}
      onSend={controls.handleSend}
      restoreDraftRequest={c.restoreDraftRequest}
      conversationStarterRequest={c.conversationStarterRequest}
      onConversationStarterApplied={c.handleConversationStarterApplied}
      onRestoreDraftApplied={c.handleRestoreDraftApplied}
      uploadEnabled={c.uploadEnabled && !c.isAgentAccessRevoked}
      onStop={c.handleStop}
      stopping={stopping}
      isRunning={!!c.pendingTaskId}
      allowSubmitWhileRunning={c.pendingTask?.supports_queue === true}
      canSteerWhileRunning={c.pendingTask?.can_supplement}
      disabled={c.isSessionArchived || c.isAgentArchived || c.isAgentAccessRevoked || !c.isAgentRuntimeBound}
      noAgent={c.noAgent}
      agentArchived={c.isAgentArchived}
      agentAccessRevoked={c.isAgentAccessRevoked}
      agentRuntimeRequired={!c.isAgentRuntimeBound}
      agentName={c.activeAgent?.name}
      agentId={c.activeAgent?.id}
      projects={c.projects}
      projectId={c.activeProjectId}
      onProjectChange={c.handleProjectChange}
      projectContextUnsupported={c.projectContextUnsupported}
      isProjectUpdating={c.isProjectUpdating}
      squads={c.squads}
      squadId={c.activeSquadId}
      onSquadChange={c.handleSquadChange}
      workspaceScopeAllowed={c.workspaceScopeAllowed}
      autonomyPolicy={controls.autonomyPolicy}
      onAutonomyPolicyChange={controls.setAutonomyPolicy}
      focusRequest={c.focusInputRequest}
      annotations={annotations}
      {...inputProps}
    />
    </>
  );
}
