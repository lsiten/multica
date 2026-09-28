"use client";

import { AudioLines, Check, LoaderCircle, Mic, RotateCcw, X } from "lucide-react";
import { useEffect, useImperativeHandle, useRef, type Ref } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { ApiError } from "@multica/core/api";
import { useTranscribeChatVoice } from "@multica/core/chat/mutations";
import type { DictationDraft } from "../../editor/dictation-draft";
import { useT } from "../../i18n";
import { useLocalVoice } from "../../platform";
import { useChatDictation } from "./use-chat-dictation";

export interface ChatVoiceInputRef {
  cancel: () => void;
  isActive: () => boolean;
}

export function ChatVoiceInput({ agentId, disabled, onBegin, onActiveChange, ref }: {
  readonly agentId: string;
  readonly disabled: boolean;
  readonly onBegin: () => DictationDraft | null;
  readonly onActiveChange: (active: boolean) => void;
  readonly ref?: Ref<ChatVoiceInputRef>;
}) {
  const { t } = useT("chat");
  const microphone = useRef<HTMLButtonElement>(null);
  const { adapter: localVoice, status: voiceStatus } = useLocalVoice();
  const transcription = useTranscribeChatVoice();
  const setupFailed = voiceStatus?.phase === "failed";
  const preparing = localVoice != null && voiceStatus?.phase !== "ready";
  const percent = voiceStatus && "percent" in voiceStatus ? voiceStatus.percent : 0;
  const dictation = useChatDictation({
    enabled: !disabled && !preparing,
    onBegin: () => { transcription.reset(); return onBegin(); },
    onActiveChange,
    onRealtimeVoice: localVoice?.transcribeStream,
    onVoice: (recording, signal) => localVoice
      ? localVoice.transcribe(recording, signal)
      : transcription.mutateAsync({ agentId, recording, signal }),
  });
  const active = dictation.phase !== "idle";
  const wasActive = useRef(false);
  useImperativeHandle(ref, () => ({ cancel: dictation.cancel, isActive: dictation.isActive }));
  useEffect(() => {
    if (wasActive.current && !active && !disabled) microphone.current?.focus();
    wasActive.current = active;
  }, [active, disabled]);
  const error = transcription.error;
  const errorMessage = error instanceof ApiError
    ? error.status === 501 || (error.status === 404 && !["agent not found", "runtime not found"].includes(error.message))
      ? t(($) => $.input.voice_upgrade_required)
      : error.message === "transcriber_unavailable"
        ? t(($) => $.input.voice_unconfigured)
        : error.message === "daemon_unavailable"
          ? t(($) => $.input.voice_offline)
          : t(($) => $.input.voice_failed)
    : t(($) => $.input.voice_failed);
  const status = dictation.phase === "requesting" ? t(($) => $.input.voice_requesting)
    : dictation.phase === "transcribing" ? t(($) => $.input.voice_transcribing)
      : localVoice?.transcribeStream ? t(($) => $.input.voice_listening)
        : t(($) => $.input.voice_recording);
  const label = setupFailed ? t(($) => $.input.voice_setup_retry)
    : preparing ? t(($) => $.input.voice_setup_progress, { percent })
      : t(($) => $.input.voice_start);
  return <>
    {active && <div className="flex flex-wrap items-center gap-2 px-3 pb-2" data-chat-dictation>
      <div className="flex min-w-0 flex-1 items-center gap-2 text-caption text-muted-foreground" role="status">
        {dictation.phase === "recording"
          ? <AudioLines aria-hidden="true" className="size-4 shrink-0 text-brand motion-safe:animate-pulse" />
          : <LoaderCircle aria-hidden="true" className="size-4 shrink-0 motion-safe:animate-spin" />}
        <span className="min-w-0 break-words">{status}</span>
        {dictation.phase === "recording" && <span className="shrink-0 font-mono tabular-nums">{String(Math.floor(dictation.seconds / 60)).padStart(2, "0")}:{String(dictation.seconds % 60).padStart(2, "0")}</span>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.input.voice_cancel)} title={t(($) => $.input.voice_cancel)} onClick={dictation.cancel}>
          <X aria-hidden="true" />
        </Button>
        <Button size="sm" variant="secondary" disabled={dictation.phase !== "recording"} onClick={dictation.stop}>
          <Check aria-hidden="true" />{t(($) => $.input.voice_done)}
        </Button>
      </div>
    </div>}
    {dictation.error && <p role="alert" className="px-3 pb-2 text-caption text-destructive">{errorMessage}</p>}
    {setupFailed && <p role="alert" className="px-3 pb-2 text-caption text-destructive">{t(($) => $.input.voice_setup_failed)}</p>}
    {!active && <Button
      ref={microphone}
      size="icon-sm" variant="ghost" disabled={disabled || (preparing && !setupFailed)}
      aria-label={label} title={label} aria-busy={preparing && !setupFailed}
      className="absolute bottom-1 right-10 overflow-hidden"
      onClick={() => setupFailed ? localVoice?.retry() : void dictation.start()}
    >
      {preparing && !setupFailed && <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 left-0 bg-muted-foreground/20 transition-[width] duration-300" style={{ width: `${percent}%` }} />}
      <span className="relative z-10 inline-flex items-center justify-center">
        {setupFailed ? <RotateCcw aria-hidden="true" /> : preparing ? `${percent}%` : <Mic aria-hidden="true" />}
      </span>
    </Button>}
  </>;
}
