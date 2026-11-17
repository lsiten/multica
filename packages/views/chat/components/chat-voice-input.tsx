"use client";

import { Check, LoaderCircle, Mic, Pause, Play, RotateCcw, X } from "@multica/ui/icons";
import { useEffect, useImperativeHandle, useRef, type Ref } from "react";
import { createPortal } from "react-dom";
import { Button } from "@multica/ui/components/ui/button";
import { ApiError } from "@multica/core/api";
import { createShortcutChord, formatShortcut, shortcutMatchesEvent } from "@multica/core/shortcuts";
import { useTranscribeChatVoice } from "@multica/core/chat/mutations";
import type { DictationDraft } from "../../editor/dictation-draft";
import { useT } from "../../i18n";
import { useLocalVoice } from "../../platform";
import { DICTATION_LIMIT_SECONDS, useChatDictation } from "./use-chat-dictation";

const DICTATION_SHORTCUT = createShortcutChord("M", { primary: true, shift: true });
const METER_BARS = [0.38, 0.62, 0.86, 0.56, 0.32, 0.7, 0.94, 0.5] as const;

export interface ChatVoiceInputRef {
  cancel: () => void;
  isActive: () => boolean;
  finish: () => Promise<boolean>;
}

export function ChatVoiceInput({ agentId, disabled, onBegin, onActiveChange, feedbackContainer, ref }: {
  readonly agentId: string;
  readonly disabled: boolean;
  readonly onBegin: () => DictationDraft | null;
  readonly onActiveChange: (active: boolean) => void;
  readonly feedbackContainer?: HTMLElement | null;
  readonly ref?: Ref<ChatVoiceInputRef>;
}) {
  const { t } = useT("chat");
  const root = useRef<HTMLDivElement>(null);
  const microphone = useRef<HTMLButtonElement>(null);
  const cancelButton = useRef<HTMLButtonElement>(null);
  const accepted = useRef(false);
  const { adapter: localVoice, status: voiceStatus } = useLocalVoice();
  const transcription = useTranscribeChatVoice();
  const setupFailed = voiceStatus?.phase === "failed";
  const preparing = localVoice != null && voiceStatus?.phase !== "ready";
  const percent = voiceStatus && "percent" in voiceStatus ? voiceStatus.percent : 0;
  const dictation = useChatDictation({
    enabled: !disabled && !preparing,
    onBegin: () => {
      transcription.reset();
      accepted.current = false;
      const draft = onBegin();
      return draft && {
        update: draft.update,
        cancel: draft.cancel,
        finish: () => { accepted.current = true; draft.finish(); },
      };
    },
    onActiveChange,
    onRealtimeVoice: localVoice?.transcribeStream,
    onVoice: (recording, signal) => localVoice
      ? localVoice.transcribe(recording, signal)
      : transcription.mutateAsync({ agentId, recording, signal }),
  });
  const active = dictation.phase !== "idle";
  const capturing = dictation.phase === "recording" || dictation.phase === "paused";
  const wasActive = useRef(false);
  useImperativeHandle(ref, () => ({ cancel: dictation.cancel, isActive: dictation.isActive, finish: dictation.finish }));
  useEffect(() => {
    if (!wasActive.current && active && document.activeElement === document.body) cancelButton.current?.focus();
    if (wasActive.current && !active && !disabled && !accepted.current) microphone.current?.focus();
    wasActive.current = active;
  }, [active, disabled]);
  const { phase, start, stop, cancel } = dictation;
  useEffect(() => {
    const composer = root.current?.closest("[data-chat-composer]") ?? root.current;
    const onKey = (event: Event) => {
      if (!(event instanceof KeyboardEvent) || event.defaultPrevented || event.repeat || event.isComposing ||
        !(event.target instanceof Node) || !composer?.contains(event.target)) return;
      if (event.key === "Escape" && phase !== "idle") {
        event.preventDefault();
        cancel();
        return;
      }
      if (disabled || preparing || !shortcutMatchesEvent(DICTATION_SHORTCUT, event)) return;
      event.preventDefault();
      if (phase === "idle") void start();
      else if (phase === "recording" || phase === "paused") stop();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [phase, disabled, preparing, start, stop, cancel]);

  const error = dictation.errorCause;
  const errorMessage = (() => {
    switch (dictation.errorKind) {
      case "permission": return t(($) => $.input.voice_permission_denied);
      case "microphone": return t(($) => $.input.voice_microphone_unavailable);
      case "unsupported": return t(($) => $.input.voice_unsupported);
      case "empty": return t(($) => $.input.voice_no_speech);
      case "transcription":
        if (error instanceof ApiError) {
          if (error.status === 501 || (error.status === 404 && !["agent not found", "runtime not found"].includes(error.message))) return t(($) => $.input.voice_upgrade_required);
          if (error.message === "transcriber_unavailable") return t(($) => $.input.voice_unconfigured);
          if (error.message === "daemon_unavailable") return t(($) => $.input.voice_offline);
        }
        return t(($) => $.input.voice_failed);
    }
  })();
  const remaining = DICTATION_LIMIT_SECONDS - dictation.seconds;
  const status = dictation.phase === "requesting" ? t(($) => $.input.voice_requesting)
    : dictation.phase === "transcribing" ? t(($) => $.input.voice_transcribing)
      : dictation.phase === "paused" ? t(($) => $.input.voice_paused)
        : remaining <= 10 ? t(($) => $.input.voice_remaining, { count: remaining })
          : localVoice?.transcribeStream ? t(($) => $.input.voice_listening) : t(($) => $.input.voice_recording);
  const label = setupFailed ? t(($) => $.input.voice_setup_retry)
    : preparing ? t(($) => $.input.voice_setup_progress, { percent }) : t(($) => $.input.voice_start);
  const done = t(($) => $.input.voice_done);
  const pauseLabel = dictation.phase === "paused" ? t(($) => $.input.voice_resume) : t(($) => $.input.voice_pause);

  const feedback = (dictation.error || setupFailed) && <div className="mx-3 mb-1 flex items-start gap-2 py-2 text-caption" data-chat-voice-error>
    <div className="min-w-0 flex-1">
      <p role="alert" className="text-destructive">{setupFailed ? t(($) => $.input.voice_setup_failed) : errorMessage}</p>
      {dictation.canRetry && <Button size="xs" variant="link" className="mt-1 pointer-coarse:min-h-11" disabled={disabled || preparing} onClick={dictation.retry}><RotateCcw aria-hidden="true" />{t(($) => $.input.voice_retry_transcription)}</Button>}
    </div>
    {!setupFailed && <Button size="icon-xs" variant="ghost" className="shrink-0 pointer-coarse:size-11" aria-label={t(($) => $.input.voice_dismiss_error)} onClick={dictation.cancel}><X aria-hidden="true" /></Button>}
  </div>;
  return <div ref={root} className="contents" data-chat-voice-input>
    {active && <div className="absolute inset-x-1.5 bottom-1 flex min-w-0 items-center gap-2" data-chat-dictation>
      <Button ref={cancelButton} size="icon-sm" variant="ghost" className="shrink-0 pointer-coarse:size-11" aria-label={t(($) => $.input.voice_cancel)} title={t(($) => $.input.voice_cancel)} onClick={dictation.cancel}>
        <X aria-hidden="true" />
      </Button>
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <span className="min-w-0 truncate text-caption text-muted-foreground" role="status" title={status}>{status}</span>
        {dictation.phase === "recording" && <span className="flex h-6 min-w-0 flex-1 items-center justify-center gap-0.5 overflow-hidden" role="img" aria-label={t(($) => $.input.voice_volume)}>
          {METER_BARS.map((base, index) => <span key={index} aria-hidden="true" className="h-4 w-0.5 shrink-0 rounded-full bg-current motion-safe:transition-transform motion-safe:duration-100" style={{ transform: `scaleY(${0.15 + base * dictation.level})` }} />)}
        </span>}
        {capturing && <span className="shrink-0 text-caption tabular-nums text-muted-foreground" aria-label={t(($) => $.input.voice_duration, { count: dictation.seconds })}>{String(Math.floor(dictation.seconds / 60)).padStart(2, "0")}:{String(dictation.seconds % 60).padStart(2, "0")}</span>}
      </div>
      {capturing && <Button size="icon-sm" variant="ghost" className="shrink-0 pointer-coarse:size-11" aria-label={pauseLabel} title={pauseLabel} onClick={dictation.togglePause}>
        {dictation.phase === "paused" ? <Play aria-hidden="true" /> : <Pause aria-hidden="true" />}
      </Button>}
      <Button size="sm" className="shrink-0 pointer-coarse:min-h-11" disabled={!capturing} aria-busy={!capturing} title={`${done} (${formatShortcut(DICTATION_SHORTCUT)})`} onClick={dictation.stop}>
        {capturing ? <Check aria-hidden="true" /> : <LoaderCircle aria-hidden="true" className="motion-safe:animate-spin" />}
        {done}
      </Button>
    </div>}
    {feedbackContainer ? createPortal(feedback, feedbackContainer) : feedback}
    {!active && <Button
      ref={microphone} size="icon-sm" variant="ghost" disabled={disabled || (preparing && !setupFailed)}
      aria-label={label} title={preparing ? label : `${label} (${formatShortcut(DICTATION_SHORTCUT)})`} aria-busy={preparing && !setupFailed}
      aria-keyshortcuts="Control+Shift+M Meta+Shift+M"
      className="relative shrink-0 overflow-hidden pointer-coarse:size-11" onClick={() => setupFailed ? localVoice?.retry() : void dictation.start()}
    >
      {preparing && !setupFailed && <span aria-hidden="true" className="pointer-events-none absolute inset-0 origin-left bg-muted-foreground/20" style={{ transform: `scaleX(${percent / 100})` }} />}
      <span className="relative inline-flex items-center justify-center">{setupFailed ? <RotateCcw aria-hidden="true" /> : preparing ? `${percent}%` : <Mic aria-hidden="true" />}</span>
    </Button>}
  </div>;
}
