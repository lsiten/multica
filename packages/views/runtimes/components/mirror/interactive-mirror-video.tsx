"use client";

import { useRef, type KeyboardEvent } from "react";
import type { VscreenSourceDescriptor } from "@multica/core/types";
import { MirrorVideo } from "./mirror-video";
import { useMirrorGestures } from "./use-mirror-gestures";
import type { MirrorControlInput } from "./video-session";

type Modifier = NonNullable<MirrorControlInput["key"]>["modifiers"] extends
  | readonly (infer M)[]
  | undefined
  ? M
  : never;

export function InteractiveMirrorVideo({
  stream,
  label,
  onFrame,
  source,
  active,
  sendInput,
}: {
  readonly stream: MediaStream;
  readonly label: string;
  readonly onFrame: () => void;
  readonly source: VscreenSourceDescriptor;
  readonly active: boolean;
  readonly sendInput: (input: MirrorControlInput) => void;
}) {
  const pointerHandlers = useMirrorGestures({ source, active, sendInput });
  const keyGestures = useRef<Map<string, string>>(new Map());

  return (
    <div
      role="application"
      aria-label={label}
      tabIndex={active ? 0 : -1}
      className={
        active
          ? "h-full w-full cursor-crosshair touch-none select-none [-webkit-touch-callout:none] focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-[-2px]"
          : "h-full w-full outline-none"
      }
      {...pointerHandlers}
      onContextMenu={(event) => active && event.preventDefault()}
      onKeyDown={(event) => {
        if (!active) return;
        const key = namedKey(event);
        if (!key) return;
        event.preventDefault();
        const gestureId =
          keyGestures.current.get(key) ?? `key-${crypto.randomUUID()}`;
        keyGestures.current.set(key, gestureId);
        sendInput({
          kind: "key:down",
          gestureId,
          key: { key, modifiers: modifiers(event) },
        });
      }}
      onKeyUp={(event) => {
        if (!active) return;
        const key = namedKey(event);
        if (!key) return;
        event.preventDefault();
        const gestureId =
          keyGestures.current.get(key) ?? `key-${crypto.randomUUID()}`;
        keyGestures.current.delete(key);
        sendInput({
          kind: "key:up",
          gestureId,
          key: { key, modifiers: modifiers(event) },
        });
      }}
    >
      <MirrorVideo stream={stream} label={label} onFrame={onFrame} ready />
    </div>
  );
}

function namedKey(event: KeyboardEvent<HTMLDivElement>): string | null {
  if (event.metaKey || event.ctrlKey || event.altKey) return null;
  const key = event.key.toLowerCase();
  if (/^[a-z0-9]$/.test(key)) return key;
  if (
    [
      "enter",
      "tab",
      " ",
      "escape",
      "backspace",
      "delete",
      "arrowleft",
      "arrowright",
      "arrowup",
      "arrowdown",
    ].includes(key)
  )
    return key === " " ? "space" : key;
  return null;
}

function modifiers(event: KeyboardEvent<HTMLDivElement>): Modifier[] {
  return [
    event.shiftKey ? "shift" : null,
    event.ctrlKey ? "control" : null,
    event.altKey ? "alt" : null,
    event.metaKey ? "meta" : null,
  ].filter((modifier): modifier is Modifier => modifier !== null);
}
