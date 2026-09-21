"use client";

import { Mic, Type } from "lucide-react";
import { useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { ApiError } from "@multica/core/api";
import { useTranscribeChatVoice } from "@multica/core/chat/mutations";
import { HoldToTalkInput } from "../../common/voice/hold-to-talk-input";
import { useT } from "../../i18n";

export function ChatVoiceInput({ agentId, disabled, onTranscript }: {
  readonly agentId: string;
  readonly disabled: boolean;
  readonly onTranscript: (text: string) => void;
}) {
  const { t } = useT("runtimes");
  const { t: tChat } = useT("chat");
  const [open, setOpen] = useState(false);
  const transcription = useTranscribeChatVoice();
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
  return <div className="relative">
    {open && !disabled && <div className="absolute bottom-full right-0 mb-2 w-64 max-w-[70vw] rounded-lg border bg-popover p-2 shadow-lg">
      <HoldToTalkInput
        enabled={!disabled}
        errorMessage={errorMessage ?? tChat(($) => $.input.voice_failed)}
        onVoice={(recording, signal) => transcription.mutateAsync({ agentId, recording, signal })}
        onTranscript={(text) => { onTranscript(text); setOpen(false); }}
      />
    </div>}
    <Button
      size="icon-sm" variant="ghost" disabled={disabled}
      aria-label={open ? t(($) => $.vscreen.switch_to_text) : t(($) => $.vscreen.switch_to_voice)}
      aria-expanded={open && !disabled}
      onClick={() => { transcription.reset(); setOpen((value) => !value); }}
    >
      {open ? <Type aria-hidden="true" /> : <Mic aria-hidden="true" />}
    </Button>
  </div>;
}
