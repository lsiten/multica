import { useRef, useState } from "react";
import { render, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { VirtuosoHandle } from "react-virtuoso";
import { useChatSearchMatch, type ChatSearchMatch } from "./use-chat-search-match";
import { revealChatSearchMatch } from "./chat-search-match";
import { readingScroller } from "./chat-reading-test-fixture";

const descriptor = Object.getOwnPropertyDescriptor(Range.prototype, "getBoundingClientRect");
afterEach(() => {
  vi.unstubAllGlobals();
  if (descriptor) Object.defineProperty(Range.prototype, "getBoundingClientRect", descriptor);
  else Reflect.deleteProperty(Range.prototype, "getBoundingClientRect");
});

function Pane({ target }: { target?: ChatSearchMatch }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const list = useRef<VirtuosoHandle | null>(null);
  useChatSearchMatch({ target, index: 0, container, list, disengage: vi.fn() });
  return <div ref={setContainer}><div data-chat-message-id="answer"><p>The matching passage.</p><p>Another matching passage.</p></div></div>;
}

it("reveals a linked message without requiring a text-search term", () => {
  const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
  const { container } = render(<div data-chat-message-id="original">Original answer</div>);
  expect(revealChatSearchMatch(container, "original", "")).toBe(true);
  expect(scroll).toHaveBeenCalledExactlyOnceWith({ block: "start", behavior: "instant" });
});

it("keeps the foreground match highlighted when an unrelated background chat unmounts", async () => {
  const highlights = new Map<string, unknown>();
  vi.stubGlobal("CSS", { highlights });
  vi.stubGlobal("Highlight", class { constructor(readonly range: Range) {} });
  Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(10, 120, 90, 17) });
  const target = { messageId: "answer", term: "matching" };
  const { rerender, unmount } = render(<><Pane key="foreground" target={target} /><Pane key="background" /></>);
  await waitFor(() => expect(highlights.has("chat-message-search")).toBe(true));

  rerender(<Pane key="foreground" target={target} />);

  expect(highlights.has("chat-message-search")).toBe(true);
  unmount();
  expect(highlights.has("chat-message-search")).toBe(false);
});

it("reveals the source when history prepend invalidates the first virtual scroll", async () => {
  const { element, anchor, list } = readingScroller({ deferFirstScroll: true });
  const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
  const view = renderHook(() => useChatSearchMatch({ target: { messageId: "saved", term: "" }, index: 0, container: element, list, disengage: vi.fn() }));

  await waitFor(() => expect(element.contains(anchor)).toBe(true));

  expect(scroll).toHaveBeenCalledWith({ block: "start", behavior: "instant" });
  view.unmount();
  element.remove();
});
