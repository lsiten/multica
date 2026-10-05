"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { humanRequestsOptions, humanRequestKeys, matchHumanTextAnswer, humanTextAnswerLabel, useHumanRequestRealtime } from "@multica/core/human-requests";
import { issueProgressKeys } from "@multica/core/issue-progress";
import { issueKeys } from "@multica/core/issues/queries";
import { chatKeys } from "@multica/core/chat/queries";
import type { HumanRequest } from "@multica/core/types/human-request";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";

export function useHumanReplyBinding(channel: "chat" | "comment", scopeId: string | null, text: string, eligible: boolean, initialRequestId = "") {
  const workspaceId = useWorkspaceId();
  const { t } = useT("common");
  const client = useQueryClient();
  const acceptedReplyId = useRef<string | null>(null);
  const filter = channel === "chat" ? { chat_session_id: scopeId ?? "" } : { issue_id: scopeId ?? "" };
  const query = useQuery({ ...humanRequestsOptions(workspaceId, filter), enabled: !!workspaceId && !!scopeId });
  useHumanRequestRealtime(workspaceId);
  const [selectedId, setSelectedId] = useState(initialRequestId);
  const [plain, setPlain] = useState(false);
  const [snapshot, setSnapshot] = useState<{ scopeId: string; text: string; request: HumanRequest } | null>(null);
  const [error, setError] = useState("");
  const knownRequests = client.getQueriesData<HumanRequest>({ queryKey: humanRequestKeys.all(workspaceId) }).flatMap(([, cached]) => cached && !Array.isArray(cached) && cached.payload && (channel === "chat" ? cached.chat_session_id : cached.issue_id) === scopeId ? [cached] : []);
  const scopedRequests = query.data ?? knownRequests;
  const available = scopedRequests.filter(request => request.status === "pending" && request.can_respond && request.payload.response_mode === "chat_or_card" && Date.parse(request.expires_at) > Date.now());
  const candidates = eligible && !plain ? available.filter(request => (!selectedId || selectedId === request.id) && !!matchHumanTextAnswer(request, text, selectedId === request.id)) : [];
  const candidate = candidates.length === 1 ? candidates[0] : undefined;
  useEffect(() => { setSnapshot(null); setSelectedId(initialRequestId); setPlain(false); setError(""); }, [scopeId, initialRequestId]);
  useEffect(() => {
    if (!eligible || plain || !scopeId || candidates.length > 1) { setSnapshot(null); return; }
    setSnapshot(previous => previous?.scopeId === scopeId && previous.text === text ? previous : candidate ? { scopeId, text, request: candidate } : null);
  }, [candidate, candidates.length, eligible, plain, scopeId, selectedId, text]);
  const current = snapshot && scopedRequests.find(request => request.id === snapshot.request.id);
  const stale = !!snapshot && (!current || current.revision !== snapshot.request.revision || current.status !== "pending" || Date.parse(current.expires_at) <= Date.now());
  const answer = snapshot && matchHumanTextAnswer(snapshot.request, text, selectedId === snapshot.request.id);
  const submit = async (raw: string): Promise<"ordinary" | "accepted" | "blocked"> => {
    if (!eligible || plain) return "ordinary";
    if (!selectedId && candidates.length > 1) { setError(t($ => $.human_request.reply_choose_target)); return "blocked"; }
    if (snapshot && raw === snapshot.text && snapshot.scopeId === scopeId && (stale || Date.parse(snapshot.request.expires_at) <= Date.now())) { setError(t($ => $.human_request.reply_stale)); return "blocked"; }
    const exact = snapshot && raw === snapshot.text && snapshot.scopeId === scopeId ? matchHumanTextAnswer(snapshot.request, raw, selectedId === snapshot.request.id) : null;
    if (!exact || !snapshot) {
      if (available.some(request => !!matchHumanTextAnswer(request, raw, selectedId === request.id))) { setError(t($ => $.human_request.reply_choose_target)); return "blocked"; }
      return "ordinary";
    }
    if (stale || !scopeId) { setError(t($ => $.human_request.reply_stale)); return "blocked"; }
    try {
      const result = await api.replyHumanRequest(snapshot.request.id, { revision: exact.revision, text: raw, channel, scope_id: scopeId });
      acceptedReplyId.current = result.reply?.reply_id ?? null;
      client.setQueryData(humanRequestKeys.detail(workspaceId, result.request.id), result.request);
      void Promise.all([
        client.invalidateQueries({ queryKey: humanRequestKeys.all(workspaceId) }),
        client.invalidateQueries({ queryKey: issueProgressKeys.all(workspaceId) }),
        client.invalidateQueries({ queryKey: channel === "chat" ? chatKeys.messages(scopeId) : issueKeys.timeline(scopeId) }),
        client.invalidateQueries({ queryKey: channel === "chat" ? chatKeys.messagesPage(scopeId) : issueKeys.tasks(scopeId) }),
        ...(channel === "chat" ? [client.invalidateQueries({ queryKey: chatKeys.pendingTask(scopeId) })] : []),
      ]);
      setError("");
      return "accepted";
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) void client.invalidateQueries({ queryKey: humanRequestKeys.all(workspaceId) });
      setError(failure instanceof ApiError && failure.status === 409 ? t($ => $.human_request.reply_stale) : failure instanceof ApiError && failure.status === 404 ? t($ => $.human_request.reply_unsupported) : t($ => $.human_request.submit_failed));
      return "blocked";
    }
  };
  const preview = available.length > 0 || snapshot ? <div className="space-y-2 px-3 py-2 text-caption" data-human-reply-binding>
    <label className="flex flex-wrap items-center gap-2"><span>{t($ => $.human_request.reply_target)}</span><select aria-label={t($ => $.human_request.reply_target)} className="min-w-0 max-w-full rounded-md border bg-background px-2 py-1 text-caption" value={selectedId} onChange={event => { setSelectedId(event.target.value); setPlain(false); setSnapshot(null); }}><option value="">{t($ => $.human_request.reply_auto)}</option>{available.map(request => <option key={request.id} value={request.id}>{request.payload.title}</option>)}</select></label>
    {snapshot && answer && <p className="break-words">{t($ => $.human_request.reply_preview, { title: snapshot.request.payload.title, answer: humanTextAnswerLabel(snapshot.request, answer) })}</p>}
    {stale && <p role="alert" className="text-destructive">{t($ => $.human_request.reply_stale)}</p>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <div className="flex flex-wrap gap-2"><Button size="sm" variant={plain ? "secondary" : "ghost"} aria-pressed={plain} onClick={() => { setPlain(!plain); setError(""); }}>{t($ => $.human_request.reply_plain)}</Button>{(stale || error) && <Button size="sm" variant="outline" onClick={() => { setError(""); void query.refetch().then(result => { const refreshed = result.data?.filter(request => !!matchHumanTextAnswer(request, text, selectedId === request.id) && (!selectedId || selectedId === request.id)) ?? []; setSnapshot(refreshed.length === 1 && scopeId ? { scopeId, text, request: refreshed[0]! } : null); }); }}>{t($ => $.human_request.refresh)}</Button>}</div>
  </div> : error ? <p role="alert" className="px-3 py-2 text-caption text-destructive">{error}</p> : null;
  return { submit, preview, acceptedReplyId, bound: !!snapshot && !!answer && !plain && candidates.length <= 1, stale };
}
