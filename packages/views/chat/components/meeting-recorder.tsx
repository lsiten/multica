"use client";

import { AudioLines, Pause, Play, Square, X } from "@multica/ui/icons";
import { useEffect, useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { useAppendMeetingSegment, useCancelMeeting, useCreateMeeting, useFinishMeeting, useTranscribeRuntimeVoice } from "@multica/core/meetings/mutations";
import { useTranscribeChatVoice } from "@multica/core/chat/mutations";
import { useT } from "../../i18n";
import { useLocalVoice } from "../../platform";
import type { Meeting } from "@multica/core/types";
import { createMeetingAudioCapture, type MeetingAudioCapture } from "./meeting-audio-capture";

type RecorderState = "idle" | "recording" | "paused" | "finishing" | "complete" | "error";
type LiveSegment = { readonly sequence: number; readonly text: string; readonly startMs: number; readonly speaker: string };

export function MeetingRecorder({
  workspaceId,
  agentId,
  chatSessionId,
  runtimeId,
  onFinished,
  onOpenMeeting,
  disabled,
}: {
  readonly workspaceId: string;
  readonly agentId?: string;
  readonly chatSessionId?: string | null;
  readonly runtimeId?: string;
  readonly onFinished?: (meeting: Meeting) => void;
  readonly onOpenMeeting?: (meetingId: string) => void;
  readonly disabled: boolean;
}) {
  const { t } = useT("chat");
  const [state, setState] = useState<RecorderState>("idle");
  const [error, setError] = useState<string>();
  const [liveText, setLiveText] = useState("");
  const [segments, setSegments] = useState<LiveSegment[]>([]);
  const [completedMeeting, setCompletedMeeting] = useState<Meeting>();
  const meetingId = useRef<string | undefined>(undefined);
  const sequence = useRef(0);
  const startedAt = useRef(0);
  const capture = useRef<MeetingAudioCapture | undefined>(undefined);
  const stopping = useRef(false);
  const paused = useRef(false);
  const finalizeCapture = useRef<(() => Promise<void>) | undefined>(undefined);
  const stream = useRef<MediaStream | undefined>(undefined);
  const abort = useRef<AbortController | undefined>(undefined);
  const pending = useRef(Promise.resolve());
  const transcriptionFailed = useRef(false);
  const createMeeting = useCreateMeeting();
  const appendSegment = useAppendMeetingSegment();
  const finishMeeting = useFinishMeeting();
  const cancelMeeting = useCancelMeeting();
  const runtimeTranscription = useTranscribeRuntimeVoice();
  const transcription = useTranscribeChatVoice();
  const { adapter: localVoice } = useLocalVoice();

  const transcribeChunk = (blob: Blob) => {
    const currentMeetingId = meetingId.current;
    const signal = abort.current?.signal;
    if (!currentMeetingId || !signal || blob.size === 0) return;
    const index = sequence.current++;
    const endMs = Math.max(0, Date.now() - startedAt.current);
    const startMs = Math.max(0, endMs - 2_000);
    pending.current = pending.current.then(async () => {
      const text = localVoice
        ? await localVoice.transcribe(blob, signal)
        : agentId
          ? await transcription.mutateAsync({ agentId, recording: blob, signal })
          : runtimeId
            ? await runtimeTranscription.mutateAsync({ runtimeId, recording: blob, signal })
            : "";
      if (!text.trim() || signal.aborted) return;
      const normalizedText = text.trim();
      setLiveText((current) => current ? `${current} ${normalizedText}` : normalizedText);
      setSegments((current) => [...current, { sequence: index, text: normalizedText, startMs, speaker: "发言人" }]);
      await appendSegment.mutateAsync({
        meetingId: currentMeetingId,
        workspace: workspaceId,
        data: {
          sequence: index,
          start_ms: startMs,
          end_ms: endMs,
          speaker_label: "",
          text: text.trim(),
          is_final: true,
        },
      });
    }).catch(() => {
      if (!signal.aborted) {
        transcriptionFailed.current = true;
        setError(t(($) => $.input.meeting_failed));
      }
    });
  };

  const stop = () => {
    if (state !== "recording" && state !== "paused") return;
    setState("finishing");
    stopping.current = true;
    void capture.current?.stop().then(() => finalizeCapture.current?.());
  };

  const togglePause = () => {
    if (state === "recording") {
      paused.current = true;
      capture.current?.pause();
      stream.current?.getTracks().forEach((track) => { track.enabled = false; });
      setState("paused");
    } else if (state === "paused") {
      paused.current = false;
      capture.current?.resume();
      stream.current?.getTracks().forEach((track) => { track.enabled = true; });
      setState("recording");
    }
  };

  const start = async () => {
    if (disabled || state !== "idle") return;
    setError(undefined);
    setLiveText("");
    setSegments([]);
    setCompletedMeeting(undefined);
    if (!navigator.mediaDevices?.getUserMedia || typeof AudioContext === "undefined") {
      setError(t(($) => $.input.voice_unsupported));
      setState("error");
      return;
    }
    let audioCapture: MeetingAudioCapture | undefined;
    try {
      const media = await navigator.mediaDevices.getUserMedia({ audio: true });
      stream.current = media;
      try {
        audioCapture = createMeetingAudioCapture({
          stream: media,
          onChunk: transcribeChunk,
          onError: () => {
            if (stopping.current) return;
            stopping.current = true;
            capture.current?.cancel();
            abort.current?.abort();
            const failedMeetingId = meetingId.current;
            meetingId.current = undefined;
            media.getTracks().forEach((track) => track.stop());
            if (failedMeetingId) void cancelMeeting.mutateAsync({ meetingId: failedMeetingId, workspace: workspaceId });
            setError(t(($) => $.input.meeting_failed));
            setState("error");
          },
        });
      } catch {
        media.getTracks().forEach((track) => track.stop());
        throw new Error("meeting audio capture unavailable");
      }
      const meeting = await createMeeting.mutateAsync({
        workspace: workspaceId,
        data: {
          source_type: chatSessionId ? "chat" : "workspace",
          source_chat_session_id: chatSessionId ?? undefined,
          source_agent_id: agentId,
          runtime_id: runtimeId,
          language: "zh",
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai",
        },
      });
      meetingId.current = meeting.id;
      sequence.current = 0;
      transcriptionFailed.current = false;
      pending.current = Promise.resolve();
      stopping.current = false;
      paused.current = false;
      startedAt.current = Date.now();
      abort.current = new AbortController();
      const finalize = async () => {
        finalizeCapture.current = undefined;
        media.getTracks().forEach((track) => track.stop());
        capture.current = undefined;
        await pending.current;
        if (meetingId.current && !abort.current?.signal.aborted) {
          if (transcriptionFailed.current) {
            const failedMeetingId = meetingId.current;
            await cancelMeeting.mutateAsync({ meetingId: failedMeetingId, workspace: workspaceId }).catch(() => undefined);
            meetingId.current = undefined;
            setState("error");
            return;
          }
          try {
            const finishedMeetingId = meetingId.current;
            const finishedMeeting = await finishMeeting.mutateAsync({ meetingId: finishedMeetingId, workspace: workspaceId });
            meetingId.current = undefined;
            onFinished?.(finishedMeeting);
            setCompletedMeeting(finishedMeeting);
            setState("complete");
          } catch {
            const failedMeetingId = meetingId.current;
            if (failedMeetingId) {
              await cancelMeeting.mutateAsync({ meetingId: failedMeetingId, workspace: workspaceId }).catch(() => undefined);
            }
            meetingId.current = undefined;
            setError(t(($) => $.input.meeting_failed));
            setState("error");
          }
        }
      };
      finalizeCapture.current = finalize;
      capture.current = audioCapture;
      audioCapture.start();
      setState("recording");
    } catch {
      audioCapture?.cancel();
      if (meetingId.current) void cancelMeeting.mutateAsync({ meetingId: meetingId.current, workspace: workspaceId });
      stream.current?.getTracks().forEach((track) => track.stop());
      setError(t(($) => $.input.voice_failed));
      setState("error");
    }
  };

  const cancel = () => {
    const currentMeetingId = meetingId.current;
    stopping.current = true;
    if (currentMeetingId) void cancelMeeting.mutateAsync({ meetingId: currentMeetingId, workspace: workspaceId });
    abort.current?.abort();
    capture.current?.cancel();
    capture.current = undefined;
    stream.current?.getTracks().forEach((track) => track.stop());
    meetingId.current = undefined;
    setState("idle");
    setError(undefined);
  };

  if (state === "complete" && completedMeeting) return <MeetingCompletedSurface meeting={completedMeeting} onClose={() => { setCompletedMeeting(undefined); setState("idle"); }} onOpenDocument={onOpenMeeting} />;
  if (state === "recording" || state === "paused" || state === "finishing") return <MeetingLiveSurface state={state} segments={segments} liveText={liveText} onPause={togglePause} onStop={stop} onCancel={cancel} />;
  return <Button size="icon-sm" variant="ghost" disabled={disabled || createMeeting.isPending} aria-label={error ?? t(($) => $.input.meeting_start)} title={error ?? t(($) => $.input.meeting_start)} onClick={state === "error" ? () => setState("idle") : () => void start()}><AudioLines aria-hidden="true" /></Button>;
}

function MeetingLiveSurface({ state, segments, liveText, onPause, onStop, onCancel }: { readonly state: RecorderState; readonly segments: readonly LiveSegment[]; readonly liveText: string; readonly onPause: () => void; readonly onStop: () => void; readonly onCancel: () => void }) {
  const { t } = useT("chat");
  const feedRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (feedRef.current) feedRef.current.scrollTop = feedRef.current.scrollHeight; }, [segments.length]);
  const paused = state === "paused";
  const finishing = state === "finishing";
  return <div className="fixed inset-0 z-50 flex min-h-0 flex-col bg-background text-foreground" data-meeting-live-surface>
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-border/70 px-5"><div className="flex items-center gap-3"><AudioLines className="text-brand" aria-hidden="true" /><div><h1 className="font-semibold">{t(($) => $.input.meeting_recording)}</h1><p className="text-caption text-muted-foreground">{paused ? t(($) => $.input.meeting_pause) : finishing ? t(($) => $.input.meeting_finishing) : t(($) => $.input.meeting_runtime)}</p></div></div><Button size="sm" variant="ghost" disabled={finishing} onClick={onCancel}><X aria-hidden="true" />{t(($) => $.input.meeting_cancel)}</Button></header>
    <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(320px,38vw)]"><section className="relative flex min-h-0 flex-col items-center justify-center overflow-hidden bg-muted/20 p-8"><div className="flex size-32 items-center justify-center rounded-full border border-brand/30 bg-brand/10"><AudioLines className="size-14 text-brand" aria-hidden="true" /></div><p className="mt-5 max-w-xl text-center text-lg text-muted-foreground">{liveText || t(($) => $.input.meeting_recording)}</p><div className="absolute bottom-8 flex items-center gap-3"><Button size="icon" variant="secondary" disabled={finishing} aria-label={paused ? t(($) => $.input.meeting_resume) : t(($) => $.input.meeting_pause)} onClick={onPause}>{paused ? <Play aria-hidden="true" /> : <Pause aria-hidden="true" />}</Button><Button size="lg" variant="destructive" disabled={finishing} onClick={onStop}><Square aria-hidden="true" />{finishing ? t(($) => $.input.meeting_finishing) : t(($) => $.input.meeting_stop)}</Button></div></section><aside className="flex min-h-0 flex-col border-l border-border/70 bg-background"><div className="border-b border-border/70 px-4 py-3"><h2 className="font-semibold">{t(($) => $.input.meeting_transcript)}</h2><p className="text-caption text-muted-foreground">{t(($) => $.input.meeting_recording)}</p></div><div ref={feedRef} className="flex min-h-0 flex-1 flex-col justify-end gap-3 overflow-y-auto p-4" aria-live="polite">{segments.map((segment) => <div key={segment.sequence} className="rounded-lg border border-border/70 bg-muted/30 p-3"><div className="mb-1 flex items-center justify-between gap-2 text-caption text-muted-foreground"><span>{segment.speaker}</span><span>{formatLiveTime(segment.startMs)}</span></div><p className="whitespace-pre-wrap text-sm leading-6">{segment.text}</p></div>)}{!segments.length && <p className="text-center text-sm text-muted-foreground">{t(($) => $.input.meeting_recording)}</p>}</div></aside></div>
  </div>;
}

function MeetingCompletedSurface({ meeting, onClose, onOpenDocument }: { readonly meeting: Meeting; readonly onClose: () => void; readonly onOpenDocument?: (meetingId: string) => void }) {
  const { t } = useT("chat");
  return <div className="fixed inset-0 z-50 flex min-h-0 flex-col bg-background" data-meeting-complete-surface><header className="flex h-14 shrink-0 items-center justify-between border-b border-border/70 px-5"><div><h1 className="font-semibold">{t(($) => $.input.meeting_page_title)}</h1><p className="text-caption text-muted-foreground">{meeting.title}</p></div><Button size="sm" variant="ghost" onClick={onClose}>{t(($) => $.input.meeting_back)}</Button></header><main className="min-h-0 flex-1 overflow-auto p-6"><article className="mx-auto max-w-3xl space-y-6 rounded-xl border border-border/70 bg-background p-6 shadow-sm"><div><h2 className="text-xl font-semibold">{meeting.title}</h2><p className="mt-1 text-sm text-muted-foreground">{meeting.topic || t(($) => $.input.meeting_empty)}</p></div><section><h3 className="font-semibold">{t(($) => $.input.meeting_summary)}</h3><p className="mt-2 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{meeting.summary || t(($) => $.input.meeting_empty)}</p></section><section><h3 className="font-semibold">{t(($) => $.input.meeting_decisions)}</h3><MeetingBulletList items={meeting.decisions} empty={t(($) => $.input.meeting_empty)} /></section><section><h3 className="font-semibold">{t(($) => $.input.meeting_action_items)}</h3><MeetingBulletList items={meeting.action_items} empty={t(($) => $.input.meeting_empty)} /></section><div className="flex flex-wrap gap-2"><Button onClick={() => onOpenDocument?.(meeting.id)} disabled={!onOpenDocument}>{t(($) => $.input.meeting_edit_document)}</Button><Button variant="secondary" onClick={onClose}>{t(($) => $.input.meeting_back)}</Button></div></article></main></div>;
}

function MeetingBulletList({ items, empty }: { readonly items: readonly string[]; readonly empty: string }) {
  return items.length ? <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-muted-foreground">{items.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul> : <p className="mt-2 text-sm text-muted-foreground">{empty}</p>;
}

function formatLiveTime(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
