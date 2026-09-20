"use client";

import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent } from "react";
import type { VscreenSourceDescriptor } from "@multica/core/types";
import { pointToSourceFrame } from "./mirror-coordinates";
import { MirrorGestures, type MirrorPointerSample } from "./mirror-gestures";
import type { MirrorControlInput } from "./video-session";

export function useMirrorGestures({ source, active, sendInput }: {
  readonly source: VscreenSourceDescriptor;
  readonly active: boolean;
  readonly sendInput: (input: MirrorControlInput) => void;
}) {
  const surface = useRef<HTMLDivElement>(null);
  const sender = useRef(sendInput);
  useLayoutEffect(() => { sender.current = sendInput; }, [sendInput]);
  const [gestures] = useState(() => new MirrorGestures((input) => sender.current(input)));

  useEffect(() => {
    const element = surface.current;
    if (!active || !element) return;
    const wheel = (event: WheelEvent) => {
      const rect = element.getBoundingClientRect();
      const point = pointToSourceFrame(event, rect, source);
      if (!point) return;
      event.preventDefault();
      // Normalize line/page wheel devices; trackpads already supply CSS pixels.
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? rect.height : 1;
      const scale = Math.min(rect.width / source.width, rect.height / source.height);
      gestures.wheel({ ...point, deltaX: event.deltaX * unit / scale, deltaY: event.deltaY * unit / scale });
    };
    const cancel = () => gestures.cancel();
    const visibility = () => { if (document.hidden) cancel(); };
    element.addEventListener("wheel", wheel, { passive: false });
    window.addEventListener("blur", cancel);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      gestures.cancel();
      element.removeEventListener("wheel", wheel);
      window.removeEventListener("blur", cancel);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, [active, gestures, source.width, source.height, source.source.sourceId, source.nativeEpoch, source.generation, source.geometryRevision]);

  const sample = (event: PointerEvent<HTMLDivElement>): MirrorPointerSample | null => {
    const point = pointToSourceFrame(event, event.currentTarget.getBoundingClientRect(), source);
    return point ? {
      id: event.pointerId, type: event.pointerType, point,
      clientX: event.clientX, clientY: event.clientY,
      button: event.button === 2 ? "right" : event.button === 1 ? "middle" : "left",
    } : null;
  };
  const cancelPointer = (event: PointerEvent<HTMLDivElement>) => {
    if (gestures.owns(event.pointerId)) gestures.cancel();
  };
  return {
    ref: surface,
    onPointerDown: (event: PointerEvent<HTMLDivElement>) => {
      if (!active) return;
      const input = sample(event);
      if (!input || !gestures.down(input)) return;
      event.preventDefault();
      event.currentTarget.setPointerCapture(event.pointerId);
      event.currentTarget.focus({ preventScroll: true });
    },
    onPointerMove: (event: PointerEvent<HTMLDivElement>) => {
      if (!active || !gestures.owns(event.pointerId)) return;
      event.preventDefault();
      const input = sample(event);
      if (input) gestures.move(input);
    },
    onPointerUp: (event: PointerEvent<HTMLDivElement>) => {
      if (!active || !gestures.owns(event.pointerId)) return;
      event.preventDefault();
      const input = sample(event);
      if (input) gestures.up(input);
      else gestures.cancel();
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    },
    onPointerCancel: cancelPointer,
    onLostPointerCapture: cancelPointer,
    onBlur: () => gestures.cancel(),
  };
}
