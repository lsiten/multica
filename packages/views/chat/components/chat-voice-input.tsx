"use client";

import { Mic, Type } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { ApiError } from "@multica/core/api";
import { useTranscribeChatVoice } from "@multica/core/chat/mutations";
import { HoldToTalkInput } from "../../common/voice/hold-to-talk-input";
import { useT } from "../../i18n";
import { useLocalVoice } from "../../platform";

export function ChatVoiceInput({ agentId, disabled, onTranscript }: {
  readonly agentId: string;
  readonly disabled: boolean;
  readonly onTranscript: (text: string) => void;
}) {
  const { t } = useT("runtimes");
  const { t: tChat } = useT("chat");
  const [open, setOpen] = useState(false);
  const voicePanel = useRef<HTMLDivElement>(null);
  const { adapter: localVoice, status: voiceStatus } = useLocalVoice();
  const transcription = useTranscribeChatVoice();
  const voiceReady = voiceStatus?.phase === "ready";
  const voicePercent = voiceStatus && "percent" in voiceStatus ? voiceStatus.percent : 0;
  const voiceSetupFailed = voiceStatus?.phase === "failed";
  const voiceUnavailable = localVoice != null && !voiceReady;
  const error = transcription.error;
  const errorMessage = error instanceof ApiError
    ? error.status === 501 || (error.status === 404 && !["agent not found", "runtime not found"].includes(error.message))
      ? tChat(($) => $.input.voice_upgrade_required)
      : error.message === "transcriber_unavailable"
        ? tChat(($) => $.input.voice_unconfigured)
        : error.message === "daemon_unavailable"
          ? tChat(($) => $.input.voice_offline)
          : undefined
    : undefined;
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  useEffect(() => {
    if (!open || disabled) return;
    voicePanel.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }, [open, disabled]);
  return <div className="relative">
    {open && !disabled && <div ref={voicePanel} role="dialog" aria-label={t(($) => $.vscreen.switch_to_voice)} className="absolute bottom-full right-0 z-20 mb-2 w-64 max-w-[calc(100vw-2rem)] rounded-lg border bg-popover p-2 shadow-lg">
      <HoldToTalkInput
        enabled={!disabled}
        onRealtimeVoice={localVoice?.transcribeStream}
        errorMessage={errorMessage ?? tChat(($) => $.input.voice_failed)}
        onVoice={(recording, signal) => localVoice
          ? localVoice.transcribe(recording, signal)
          : transcription.mutateAsync({ agentId, recording, signal })}
        onTranscript={(text) => { onTranscript(text); setOpen(false); }}
      />
    </div>}
    <Button
      size="icon-sm" variant="ghost" disabled={disabled || voiceUnavailable}
      aria-label={voiceReady || localVoice == null ? (open ? t(($) => $.vscreen.switch_to_text) : t(($) => $.vscreen.switch_to_voice)) : voiceSetupFailed ? tChat(($) => $.input.voice_setup_failed) : `Voice setup ${voicePercent}%`}
      aria-expanded={open && !disabled && voiceReady}
      aria-busy={voiceUnavailable}
      title={voiceSetupFailed ? tChat(($) => $.input.voice_setup_failed) : undefined}
      className="relative overflow-hidden"
      onClick={() => { transcription.reset(); setOpen((value) => !value); }}
    >
      {voiceUnavailable && <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 left-0 bg-muted-foreground/20 transition-[width] duration-300" style={{ width: `${voicePercent}%` }} />}
      <span className="relative z-10 inline-flex items-center justify-center">
        {voiceUnavailable ? `${voicePercent}%` : open ? <Type aria-hidden="true" /> : <Mic aria-hidden="true" />}
      </span>
    </Button>
  </div>;
}
