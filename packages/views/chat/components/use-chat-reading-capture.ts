"use client";

import { useCallback, useLayoutEffect, useRef } from "react";
import { useChatReadingPositionStore, type ChatReadingPosition } from "@multica/core/chat";
import { captureChatReadingPosition, chatScrollerVisible, type ChatReadingRow } from "./chat-reading-position";

export function useChatReadingCapture(input: {
  readonly scope: string | null;
  readonly container: HTMLElement | null;
  readonly active: boolean;
  readonly ready: boolean;
  readonly rows: readonly ChatReadingRow[];
  readonly following: () => boolean;
  readonly onVisible: (position: ChatReadingPosition) => void;
  readonly onNavigate: () => void;
}) {
  const latest = useRef(input);
  latest.current = input;
  const ownsInput = useRef(false);
  const { scope, container, active } = input;
  const takeOwnership = useCallback(() => { ownsInput.current = true; }, []);
  useLayoutEffect(() => {
    if (!scope || !container || !active) return;
    let frame: number | null = null;
    const save = () => {
      frame = null;
      const state = latest.current;
      if (!state.ready || !ownsInput.current) return;
      const position = captureChatReadingPosition(container, state.rows, state.following());
      if (position) useChatReadingPositionStore.getState().setPosition(scope, position);
    };
    const scroll = () => { if (frame === null) frame = requestAnimationFrame(save); };
    const own = (event: Event) => {
      ownsInput.current = event.target instanceof Node && container.contains(event.target);
      if (ownsInput.current) latest.current.onNavigate();
    };
    let visible = chatScrollerVisible(container);
    const observer = new ResizeObserver(() => {
      const next = chatScrollerVisible(container);
      if (next && !visible) {
        const position = useChatReadingPositionStore.getState().positions[scope];
        if (position) latest.current.onVisible(position);
      }
      visible = next;
    });
    observer.observe(container);
    container.addEventListener("scroll", scroll, { passive: true });
    for (const event of ["pointerdown", "wheel", "touchstart", "keydown", "focusin"]) document.addEventListener(event, own, { capture: true, passive: true });
    return () => {
      container.removeEventListener("scroll", scroll);
      for (const event of ["pointerdown", "wheel", "touchstart", "keydown", "focusin"]) document.removeEventListener(event, own, { capture: true });
      observer.disconnect();
      if (frame !== null) cancelAnimationFrame(frame);
      save();
    };
  }, [scope, container, active]);
  return takeOwnership;
}
