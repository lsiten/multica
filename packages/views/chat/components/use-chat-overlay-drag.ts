"use client";

import { useEffect, useRef, type KeyboardEvent, type PointerEvent, type RefObject } from "react";
import { useChatWorkspaceLayoutStore, DEFAULT_CHAT_WORKSPACE_LAYOUT } from "@multica/core/chat";

export function useChatOverlayDrag(scope: string, container: RefObject<HTMLDivElement | null>) {
  const finishRef = useRef<(() => void) | null>(null);
  const setLayout = useChatWorkspaceLayoutStore((state) => state.setLayout);
  useEffect(() => () => finishRef.current?.(), [scope]);

  const geometry = () => {
    const root = container.current;
    const panel = root?.querySelector<HTMLElement>("[data-chat-pane=conversation]");
    if (!root || !panel) return null;
    const bounds = panel.offsetParent instanceof HTMLElement
      ? panel.offsetParent.getBoundingClientRect()
      : root.getBoundingClientRect();
    const rect = panel.getBoundingClientRect();
    const inspectorWidth = root.querySelector<HTMLElement>("[data-chat-inspector]")?.getBoundingClientRect().width ?? 0;
    return { root, x: rect.left - bounds.left, y: rect.top - bounds.top, maxX: Math.max(0, bounds.width - rect.width - inspectorWidth), maxY: Math.max(0, bounds.height - rect.height) };
  };
  const commit = (x: number, y: number) => {
    const layout = useChatWorkspaceLayoutStore.getState().layouts[scope] ?? DEFAULT_CHAT_WORKSPACE_LAYOUT;
    setLayout(scope, { overlay: { x, y, collapsed: layout.overlay?.collapsed ?? false } });
  };
  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    const start = geometry();
    if (!start) return;
    event.preventDefault();
    finishRef.current?.();
    const handle = event.currentTarget;
    const pointerId = event.pointerId;
    const origin = { x: event.clientX, y: event.clientY };
    let latest = { x: start.x, y: start.y };
    handle.setPointerCapture(pointerId);
    const move = (next: globalThis.PointerEvent) => {
      latest = {
        x: Math.max(0, Math.min(start.maxX, start.x + next.clientX - origin.x)),
        y: Math.max(0, Math.min(start.maxY, start.y + next.clientY - origin.y)),
      };
      start.root.style.setProperty("--chat-overlay-x", `${latest.x}px`);
      start.root.style.setProperty("--chat-overlay-y", `${latest.y}px`);
    };
    const finish = (save: boolean) => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", cancel);
      window.removeEventListener("blur", cancel);
      finishRef.current = null;
      if (handle.hasPointerCapture(pointerId)) handle.releasePointerCapture(pointerId);
      if (save) commit(latest.x, latest.y);
      else {
        start.root.style.setProperty("--chat-overlay-x", `${start.x}px`);
        start.root.style.setProperty("--chat-overlay-y", `${start.y}px`);
      }
    };
    const up = () => finish(true);
    const cancel = () => finish(false);
    finishRef.current = cancel;
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", cancel);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    const current = geometry();
    if (!current) return;
    let { x, y } = current;
    switch (event.key) {
      case "ArrowLeft": x -= 16; break;
      case "ArrowRight": x += 16; break;
      case "ArrowUp": y -= 16; break;
      case "ArrowDown": y += 16; break;
      case "Home": x = 0; y = 0; break;
      default: return;
    }
    event.preventDefault();
    commit(Math.max(0, Math.min(current.maxX, x)), Math.max(0, Math.min(current.maxY, y)));
  };
  const toggleCollapsed = () => {
    const current = geometry();
    const layout = useChatWorkspaceLayoutStore.getState().layouts[scope] ?? DEFAULT_CHAT_WORKSPACE_LAYOUT;
    setLayout(scope, { overlay: { x: current?.x ?? 16, y: current?.y ?? 16, collapsed: !layout.overlay?.collapsed } });
  };
  return { onPointerDown, onKeyDown, toggleCollapsed };
}
