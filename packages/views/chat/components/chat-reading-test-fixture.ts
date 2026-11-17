import type { VirtuosoHandle } from "react-virtuoso";

export function readingScroller({ deferFirstScroll = false }: { readonly deferFirstScroll?: boolean } = {}) {
  const element = document.createElement("div");
  let visible = true;
  const viewport = new DOMRect(0, 0, 600, 400);
  Object.defineProperties(element, {
    clientHeight: { get: () => visible ? 400 : 0 },
    clientWidth: { get: () => visible ? 600 : 0 },
    scrollHeight: { get: () => 2400 },
  });
  element.getBoundingClientRect = () => viewport;
  element.getClientRects = () => {
    const boxes = visible ? [viewport] : [];
    return { ...boxes, length: boxes.length, item: (index: number) => boxes[index] ?? null, [Symbol.iterator]: () => boxes[Symbol.iterator]() };
  };
  document.body.append(element);
  const anchor = document.createElement("div");
  anchor.dataset.chatMessageId = "saved";
  anchor.getBoundingClientRect = () => new DOMRect(0, 650 - element.scrollTop, 600, 900);
  let deferred = deferFirstScroll;
  const handle: VirtuosoHandle = {
    autoscrollToBottom() {}, getState() {}, scrollBy() {}, scrollIntoView() {}, scrollTo() {},
    scrollToIndex(location) {
      if (deferred) { deferred = false; return; }
      const index = typeof location === "number" ? location : location.index;
      if (index === 0) { element.append(anchor); element.scrollTop = 650; }
    },
  };
  return { element, anchor, list: { current: handle }, hide: () => { visible = false; } };
}
