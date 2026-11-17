"use client";

import { useLayoutEffect, useRef } from "react";
import { useChatWorkspaceLayoutStore } from "@multica/core/chat";

export function useDocumentReadingPosition(scope: string, ready: boolean, legacyScope?: string) {
  const container = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const element = container.current;
    if (!element || !ready) return;
    const positions = useChatWorkspaceLayoutStore.getState().readingPositions;
    element.scrollTop = positions[scope] ?? (legacyScope ? positions[legacyScope] : undefined) ?? 0;
    let frame: number | null = null;
    const save = () => {
      frame = null;
      useChatWorkspaceLayoutStore.getState().setReadingPosition(scope, element.scrollTop);
    };
    const onScroll = () => { if (frame === null) frame = requestAnimationFrame(save); };
    element.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      element.removeEventListener("scroll", onScroll);
      if (frame !== null) cancelAnimationFrame(frame);
      save();
    };
  }, [scope, ready, legacyScope]);
  return container;
}
