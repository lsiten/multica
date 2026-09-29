"use client";

import { ArrowUp, LoaderCircle, Mic, RotateCcw, Square, X } from "lucide-react";
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
  finish: () => Promise<boolean>;
}

export function ChatVoiceInput({ agentId, disabled, onBegin, onActiveChange, onSubmit, canSubmit = false, hasContent = false, ref }: {
  readonly agentId: string;
  readonly disabled: boolean;
  readonly onBegin: () => DictationDraft | null;
  readonly onActiveChange: (active: boolean) => void;
  readonly onSubmit?: () => void;
  readonly canSubmit?: boolean;
  readonly hasContent?: boolean;
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
  useImperativeHandle(ref, () => ({ cancel: dictation.cancel, isActive: dictation.isActive, finish: dictation.finish }));
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
  const meterBars = [0.38, 0.62, 0.86, 0.56, 0.32, 0.7, 0.94, 0.5, 0.74, 0.42, 0.82, 0.58];
  return <>
    {active && <div className="absolute bottom-1 inset-x-1.5 flex min-w-0 items-center gap-2" data-chat-dictation>
      <Button size="icon-sm" variant="outline" className="shrink-0 rounded-full" aria-label={t(($) => $.input.voice_cancel)} title={t(($) => $.input.voice_cancel)} onClick={dictation.cancel}>
        <X aria-hidden="true" />
      </Button>
      <div className="flex min-w-0 flex-1 items-center gap-2">
      <span className="truncate text-caption text-muted-foreground" role="status" title={status}>{status}</span>
      <span
        className="flex h-8 min-w-0 flex-1 items-center justify-center gap-0.5 overflow-hidden text-foreground"
        role="img"
        aria-label={t(($) => $.input.voice_volume)}
      >
        {meterBars.map((base, index) => {
          const scale = 0.5 + base * (dictation.phase === "recording" ? dictation.level : 0);
          return <span key={index} aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-current motion-safe:transition-transform motion-safe:duration-100" style={{ transform: `scale(${scale})` }} />;
        })}
      </span>
      {dictation.phase === "recording" && <span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground" aria-hidden="true">{String(Math.floor(dictation.seconds / 60)).padStart(2, "0")}:{String(dictation.seconds % 60).padStart(2, "0")}</span>}
      </div>
      <Button
        size="icon-sm"
        variant="outline"
        className="shrink-0 rounded-full"
        aria-label={t(($) => $.input.voice_stop)}
        title={t(($) => $.input.voice_stop)}
        disabled={dictation.phase !== "recording"}
        aria-busy={dictation.phase !== "recording"}
        onClick={dictation.stop}
      >
        {dictation.phase === "transcribing" || dictation.phase === "requesting"
          ? <LoaderCircle aria-hidden="true" className="motion-safe:animate-spin" />
          : <Square aria-hidden="true" className="size-3.5 fill-current" />}
      </Button>
      <Button size="icon-sm" className="shrink-0 rounded-full" aria-label={t(($) => $.input.voice_send)} title={t(($) => $.input.voice_send)} disabled={!canSubmit || dictation.phase !== "recording" || (!hasContent && !dictation.hasTranscript && !!localVoice?.transcribeStream)} onClick={onSubmit}>
        <ArrowUp aria-hidden="true" />
      </Button>
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
