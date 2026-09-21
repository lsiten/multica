"use client";

import type { TFunction } from "i18next";
import { Mic, MousePointer2, OctagonX, SquareMousePointer, Type } from "lucide-react";
import { useState } from "react";
import type {
  RuntimeDevice,
  VscreenCommandKind,
  VscreenScope,
  VscreenSourceDescriptor,
  VscreenStateSnapshot,
} from "@multica/core/types";
import { VSCREEN_CAPABILITIES } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../../i18n";
import type { MirrorControlState } from "./video-session";
import { appSwitchModifiers } from "./mirror-shortcuts";
import { HoldToTalkInput } from "../../../common/voice/hold-to-talk-input";

export function MirrorControlBar({
  scope,
  runtime,
  source,
  state,
  videoReady,
  control,
  onCommand,
  onStartControl,
  onStopControl,
  onType,
  onKey,
  clientOS,
  onVoice,
  voiceTranscript,
  agents = [],
  agentId = "",
  onAgentChange,
  onAgentMessage,
}: {
  readonly scope: VscreenScope;
  readonly runtime: RuntimeDevice;
  readonly source: VscreenSourceDescriptor | null;
  readonly state: VscreenStateSnapshot | null;
  readonly videoReady: boolean;
  readonly control: MirrorControlState;
  readonly onCommand: (kind: VscreenCommandKind) => void;
  readonly onStartControl: () => void;
  readonly onStopControl: () => void;
  readonly onType: (text: string) => void;
  readonly onKey: (key: string, modifiers?: ("shift" | "control" | "alt" | "meta")[]) => void;
  readonly clientOS?: string;
  readonly onVoice: (recording: Blob) => Promise<void> | void;
  readonly voiceTranscript?: string;
  readonly agents?: readonly { readonly id: string; readonly name: string }[];
  readonly agentId?: string;
  readonly onAgentChange?: (agentId: string) => void;
  readonly onAgentMessage?: (agentId: string, text: string) => Promise<void> | void;
}) {
  const { t } = useT("runtimes");
  const [text, setText] = useState("");
  const [inputMode, setInputMode] = useState<"text" | "voice">("text");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState(false);
  const owner = runtime.owner_id === scope.accountId;
  const capabilities = Array.isArray(runtime.metadata.capabilities)
    ? runtime.metadata.capabilities.filter(
        (capability): capability is string => typeof capability === "string",
      )
    : [];
  const capable = capabilities.includes(VSCREEN_CAPABILITIES.screenControl);
  const physical = source?.source.kind !== "virtual";
  const accessibilityReady =
    !physical || state?.permissions.accessibility === "granted";
  const canRequest =
    capable &&
    videoReady &&
    !!source &&
    !!state?.humanInteraction &&
    accessibilityReady;
  const active = control.status === "active";
  const requesting = control.status === "requesting";
  const feedback = control.lastAck?.reason === "busy"
    ? t(($) => $.vscreen.control_busy)
    : control.status === "failed" || control.reason
      ? controlReason(t, control.reason)
      : active
        ? t(($) => $.vscreen.controlling)
        : state?.humanInteraction
          ? t(($) => $.vscreen.view_only)
          : t(($) => $.vscreen.interaction_disabled);

  const selectedAgent = agents.find((agent) => agent.id === agentId);
  const agentRoute = !!selectedAgent && !!onAgentMessage;
  const canSend = active || agentRoute;

  const sendType = async () => {
    const value = text;
    if (!value || !canSend || sending) return;
    setSending(true);
    setSendError(false);
    try {
      if (agentRoute) await onAgentMessage?.(agentId, value);
      else onType(value);
      setText("");
    } catch {
      setSendError(true);
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="space-y-2 border-t p-3">
      <div className="flex flex-wrap items-center gap-2">
        {active ? (
          <Button size="sm" variant="destructive" onClick={onStopControl}>
            <SquareMousePointer className="size-4" />
            {t(($) => $.vscreen.stop_control)}
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={!canRequest || requesting}
            onClick={onStartControl}
          >
            <MousePointer2 className="size-4" />
            {requesting
              ? t(($) => $.vscreen.requesting_control)
              : t(($) => $.vscreen.start_control)}
          </Button>
        )}
        {owner && (
          <>
            <Button
              size="sm"
              variant="outline"
              disabled={!capable || state?.humanInteraction}
              onClick={() => onCommand("enable_interaction")}
            >
              {t(($) => $.vscreen.enable_interaction)}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={!capable || !state?.humanInteraction}
              onClick={() => onCommand("disable_interaction")}
            >
              {t(($) => $.vscreen.disable_interaction)}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={!capable}
              onClick={() => onCommand("emergency_stop")}
            >
              <OctagonX className="size-4" />
              {t(($) => $.vscreen.emergency_stop)}
            </Button>
          </>
        )}
        <span role="status" className="text-caption text-muted-foreground">
          {feedback}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {inputMode === "text" ? (
        <label className="flex min-w-56 flex-1 items-center gap-2 text-caption">
          <Type className="size-4 shrink-0" />
          <span className="sr-only">{t(($) => $.vscreen.external_text)}</span>
          <Input
            value={text}
            maxLength={1024}
            autoComplete="off"
            disabled={sending}
            onChange={(event) => {
              setText(event.target.value.slice(0, 1024));
              if (sendError) setSendError(false);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.nativeEvent.isComposing) {
                event.preventDefault();
                void sendType();
              }
            }}
            className="h-8 min-w-0 flex-1"
            placeholder={t(($) => $.vscreen.external_text)}
          />
        </label>
        ) : (
          <HoldToTalkInput
            enabled={canSend && videoReady && !sending}
            transcript={voiceTranscript}
            onVoice={onVoice}
            onTranscript={(value) => {
              setText((draft) => draft ? `${draft} ${value}` : value);
              setInputMode("text");
            }}
          />
        )}
        {inputMode === "text" && <Button
          size="sm"
          variant="secondary"
          disabled={!text || !canSend || sending}
          aria-busy={sending}
          onClick={() => void sendType()}
        >
          {sending ? t(($) => $.vscreen.sending) : t(($) => $.vscreen.type_text)}
        </Button>}
        {agents.length > 0 && (
          <select
            aria-label={t(($) => $.vscreen.send_target)}
            value={agentId}
            disabled={sending}
            onChange={(event) => {
              onAgentChange?.(event.target.value);
              setSendError(false);
            }}
            className="h-8 rounded-md border bg-background px-2 text-caption"
          >
            <option value="">{t(($) => $.vscreen.send_screen)}</option>
            {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
          </select>
        )}
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setInputMode((mode) => mode === "text" ? "voice" : "text");
          }}
          aria-label={inputMode === "text" ? t(($) => $.vscreen.switch_to_voice) : t(($) => $.vscreen.switch_to_text)}
        >
          {inputMode === "text" ? <Mic className="size-4" /> : <Type className="size-4" />}
        </Button>
        {sendError && (
          <span role="alert" className="text-caption text-destructive">
            {t(($) => $.vscreen.send_failed)}
          </span>
        )}
      </div>
      {active && (
        <div className="flex flex-wrap items-center gap-2">
          {(
            [
              ["escape", "Esc"],
              ["enter", "Enter"],
              ["tab", "Tab"],
            ] as const
          ).map(([key, label]) => (
            <Button
              key={key}
              size="sm"
              variant="outline"
              onClick={() => onKey(key)}
            >
              {label}
            </Button>
          ))}
          <Button size="sm" variant="outline" onClick={() => onKey("backspace")}>
            {t(($) => $.vscreen.backspace)}
          </Button>
          <Button size="sm" variant="outline" onClick={() => onKey("c", ["control"])}>
            {t(($) => $.vscreen.copy_shortcut)}
          </Button>
          <Button size="sm" variant="outline" onClick={() => onKey("v", ["control"])}>
            {t(($) => $.vscreen.paste_shortcut)}
          </Button>
          <Button size="sm" variant="outline" onClick={() => onKey("tab", [...appSwitchModifiers(clientOS)])}>
            {t(($) => $.vscreen.switch_app)}
          </Button>
        </div>
      )}
    </div>
  );
}

function controlReason(
  t: TFunction<"runtimes">,
  reason: string | undefined,
): string {
  switch (reason) {
    case "interaction_disabled":
      return t(($) => $.vscreen.interaction_disabled);
    case "stale":
    case "stale_generation":
    case "source_unavailable":
      return t(($) => $.vscreen.control_stale);
    case "upgrade_required":
      return t(($) => $.vscreen.upgrade);
    case "denied":
    case "permission_denied":
      return t(($) => $.vscreen.control_denied);
    default:
      return t(($) => $.vscreen.control_failed);
  }
}
