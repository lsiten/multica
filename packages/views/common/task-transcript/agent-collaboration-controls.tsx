"use client";

import { useState } from "react";
import { api } from "@multica/core/api";
import type { AgentTask } from "@multica/core/types/agent";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

function requestId() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * The run inspector is the shared Web/Desktop collaboration surface. It keeps
 * the target Run visible, makes append versus next explicit, and leaves the
 * operation receipt on screen so a retry never looks like a duplicate send.
 */
export function AgentCollaborationControls({ task, agentName, runtimeName }: { task: AgentTask; agentName?: string; runtimeName?: string }) {
  const { t } = useT("agents");
  const [mode, setMode] = useState<"append" | "next" | "delegate">("append");
  const [content, setContent] = useState("");
  const [targetAgentId, setTargetAgentId] = useState("");
  const [sendRequestId, setSendRequestId] = useState("");
  const [watchRequestId, setWatchRequestId] = useState("");
  const [stopRequestId, setStopRequestId] = useState("");
  const [pending, setPending] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const terminal = ["completed", "failed", "cancelled"].includes(task.status);
  if (!task.issue_id) return null;

  const send = async () => {
    const text = content.trim();
    if (!text || pending) return;
    setPending(true);
    setError("");
    const id = sendRequestId || requestId();
    if (!sendRequestId) setSendRequestId(id);
    try {
      const operation = mode === "append"
        ? await api.agentCollaboration.append(task.id, { content: text, clientRequestId: id })
        : mode === "next"
          ? await api.agentCollaboration.next(task.id, { content: text, clientRequestId: id })
          : await api.agentCollaboration.delegate(task.issue_id, { targetAgentId: targetAgentId.trim(), content: text, clientRequestId: id });
      setStatus(operation.status);
      setContent("");
      setSendRequestId("");
    } catch {
      setError(t(($) => $.creation_studio.builder.send_failed));
    } finally {
      setPending(false);
    }
  };

  const watch = async () => {
    if (pending) return;
    setPending(true);
    setError("");
    const id = watchRequestId || requestId();
    if (!watchRequestId) setWatchRequestId(id);
    try {
      const operation = await api.agentCollaboration.watch(task.id, id);
      setStatus(operation.status);
      setWatchRequestId("");
    } catch {
      setError(t(($) => $.creation_studio.builder.send_failed));
    } finally {
      setPending(false);
    }
  };

  const stop = async () => {
    if (pending || terminal) return;
    setPending(true);
    setError("");
    const id = stopRequestId || requestId();
    if (!stopRequestId) setStopRequestId(id);
    try {
      const operation = await api.agentCollaboration.stop(task.id, id);
      setStatus(operation.status);
      setStopRequestId("");
    } catch {
      setError(t(($) => $.creation_studio.builder.stop_failed));
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="space-y-2 border-b bg-muted/20 px-4 py-3" data-testid="agent-collaboration-controls">
      <div className="flex flex-wrap items-center gap-2">
        <label className="text-caption text-muted-foreground" htmlFor={`collaboration-mode-${task.id}`}>
          {agentName || task.agent_id}{runtimeName ? ` · ${runtimeName}` : ""} · {t(($) => $.collaboration.mode_label)}
        </label>
        <select
          id={`collaboration-mode-${task.id}`}
          aria-label={t(($) => $.collaboration.mode_label)}
          className="h-8 rounded-md border bg-background px-2 text-caption"
          value={mode}
          onChange={(event) => setMode(event.target.value as "append" | "next" | "delegate")}
          disabled={pending}
        >
          <option value="append">{t(($) => $.collaboration.append)}</option>
          <option value="next">{t(($) => $.collaboration.next)}</option>
          <option value="delegate">{t(($) => $.collaboration.delegate)}</option>
        </select>
        <span className="text-micro text-muted-foreground">{t(($) => $.collaboration.run_prefix)} {task.id}</span>
        {status && <span role="status" className="text-micro text-muted-foreground">{status}</span>}
      </div>
      {mode === "delegate" && (
        <input
          value={targetAgentId}
          onChange={(event) => setTargetAgentId(event.target.value)}
          disabled={pending}
          placeholder={t(($) => $.collaboration.target_agent_placeholder)}
          aria-label={t(($) => $.collaboration.target_agent)}
          className="h-8 w-full rounded-md border bg-background px-2 text-caption"
        />
      )}
      <div className="flex flex-wrap items-end gap-2">
        <textarea
          value={content}
          onChange={(event) => setContent(event.target.value)}
          disabled={pending || (terminal && mode === "append")}
          rows={2}
          placeholder={t(($) => $.creation_studio.builder.placeholder)}
          aria-label={t(($) => $.creation_studio.builder.placeholder)}
          className="min-h-12 min-w-[16rem] flex-1 resize-y rounded-md border bg-background px-2 py-1.5 text-caption"
        />
        <Button type="button" size="sm" onClick={() => void send()} disabled={pending || !content.trim() || (mode === "delegate" && !targetAgentId.trim()) || (terminal && mode === "append")} aria-busy={pending}>
          {pending ? t(($) => $.collaboration.sending) : t(($) => $.creation_studio.builder.send)}
        </Button>
        {!terminal && (
          <>
            <Button type="button" size="sm" variant="outline" onClick={() => void watch()} disabled={pending} aria-busy={pending}>
              {t(($) => $.collaboration.watch)}
            </Button>
            <Button type="button" size="sm" variant="destructive" onClick={() => void stop()} disabled={pending} aria-busy={pending}>
              {t(($) => $.collaboration.stop)}
            </Button>
          </>
        )}
      </div>
      {error && <p role="alert" className="text-caption text-destructive">{error}</p>}
    </div>
  );
}
