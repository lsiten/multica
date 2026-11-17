// @vitest-environment jsdom
import { act, fireEvent, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useChatReadingPositionStore } from "@multica/core/chat";
import { useChatReadingCapture } from "./use-chat-reading-capture";
import { readingScroller } from "./chat-reading-test-fixture";

beforeEach(() => {
  useChatReadingPositionStore.setState({ positions: {} });
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 0));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
});
afterEach(() => { vi.unstubAllGlobals(); document.body.replaceChildren(); });

it("keeps the reader's anchor when a hidden panel clamps its scroll position", async () => {
  const { element, anchor, hide } = readingScroller();
  element.append(anchor);
  const scope = JSON.stringify(["workspace", "session"]);
  const onNavigate = vi.fn();
  let following = false;
  const view = renderHook(() => useChatReadingCapture({ scope, container: element, active: true, ready: true, rows: [{ messageId: "saved", createdAt: "2026-11-16T01:00:00Z", index: 0 }], following: () => following, onVisible: vi.fn(), onNavigate }));
  fireEvent.wheel(element, { deltaY: 180 });
  element.scrollTop = 830;
  fireEvent.scroll(element);
  await waitFor(() => expect(useChatReadingPositionStore.getState().positions[scope]).toMatchObject({ messageId: "saved", offset: 180 }));

  hide();
  following = true;
  element.scrollTop = 0;
  fireEvent.scroll(element);
  await act(async () => view.unmount());

  expect(useChatReadingPositionStore.getState().positions[scope]).toEqual({ kind: "message", messageId: "saved", offset: 180, createdAt: "2026-11-16T01:00:00Z" });
});
