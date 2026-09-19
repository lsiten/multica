"use client";

import type { TFunction } from "i18next";
import { MousePointer2, OctagonX, SquareMousePointer, Type } from "lucide-react";
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
}) {
  const { t } = useT("runtimes");
  const [text, setText] = useState("");
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

  const sendType = () => {
    const value = text;
    if (!value || !active) return;
    onType(value);
    setText("");
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
      {active && (
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex min-w-56 flex-1 items-center gap-2 text-caption">
            <Type className="size-4 shrink-0" />
            <span className="sr-only">{t(($) => $.vscreen.external_text)}</span>
            <Input
              value={text}
              maxLength={1024}
              autoComplete="off"
              onChange={(event) => setText(event.target.value.slice(0, 1024))}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  sendType();
                }
              }}
              className="h-8 min-w-0 flex-1"
              placeholder={t(($) => $.vscreen.external_text)}
            />
          </label>
          <Button size="sm" variant="secondary" disabled={!text} onClick={sendType}>
            {t(($) => $.vscreen.type_text)}
          </Button>
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
          <Button
            size="sm"
            variant="outline"
            onClick={() => onKey("c", ["meta"])}
          >
            {t(($) => $.vscreen.copy_shortcut)}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => onKey("v", ["meta"])}
          >
            {t(($) => $.vscreen.paste_shortcut)}
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
