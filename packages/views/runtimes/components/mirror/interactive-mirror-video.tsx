"use client";

import { useRef, type KeyboardEvent, type PointerEvent, type WheelEvent } from "react";
import type { VscreenSourceDescriptor } from "@multica/core/types";
import { MirrorVideo } from "./mirror-video";
import { pointToSourceFrame, type SourcePoint } from "./mirror-coordinates";
import type { MirrorControlInput, MirrorControlPointer } from "./video-session";

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
  const activePointerId = useRef<number | null>(null);
  const pointerGesture = useRef<string | null>(null);
  const lastPointerPoint = useRef<SourcePoint | null>(null);
  const keyGestures = useRef<Map<string, string>>(new Map());
  const pointerButton = useRef<MirrorControlPointer["button"]>("left");

  const pointFromEvent = (
    event: PointerEvent<HTMLDivElement> | WheelEvent<HTMLDivElement>,
  ): SourcePoint | null =>
    pointToSourceFrame(
      { clientX: event.clientX, clientY: event.clientY },
      event.currentTarget.getBoundingClientRect(),
      source,
    );

  const sendPointer = (
    kind: MirrorControlInput["kind"],
    event: PointerEvent<HTMLDivElement>,
  ): SourcePoint | null => {
    const point = pointFromEvent(event) ?? lastPointerPoint.current;
    if (!pointerGesture.current || !point) return null;
    const pointer: MirrorControlPointer = {
      ...point,
      button: pointerButton.current,
    };
    sendInput({ kind, gestureId: pointerGesture.current, pointer });
    return point;
  };

  const endPointer = (event: PointerEvent<HTMLDivElement>) => {
    if (!active || activePointerId.current !== event.pointerId) return;
    event.preventDefault();
    const point = sendPointer("pointer:up", event);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    activePointerId.current = null;
    pointerGesture.current = null;
    lastPointerPoint.current = point;
  };

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
      onPointerDown={(event) => {
        if (!active || activePointerId.current !== null) return;
        const point = pointFromEvent(event);
        if (!point) return;
        event.preventDefault();
        activePointerId.current = event.pointerId;
        event.currentTarget.setPointerCapture(event.pointerId);
        event.currentTarget.focus({ preventScroll: true });
        pointerButton.current =
          event.button === 2 ? "right" : event.button === 1 ? "middle" : "left";
        pointerGesture.current = crypto.randomUUID();
        lastPointerPoint.current = point;
        sendPointer("pointer:down", event);
      }}
      onPointerMove={(event) => {
        if (!active || activePointerId.current !== event.pointerId) return;
        event.preventDefault();
        const point = sendPointer("pointer:move", event);
        if (point) lastPointerPoint.current = point;
      }}
      onPointerUp={endPointer}
      onPointerCancel={endPointer}
      onContextMenu={(event) => active && event.preventDefault()}
      onWheel={(event) => {
        if (!active) return;
        const point = pointFromEvent(event);
        if (!point) return;
        event.preventDefault();
        sendInput({
          kind: "wheel",
          gestureId: crypto.randomUUID(),
          pointer: {
            ...point,
            deltaX: event.deltaX,
            deltaY: event.deltaY,
          },
        });
      }}
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
