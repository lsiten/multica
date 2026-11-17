"use client";

import { useEffect, useRef, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import { clearChatSearchMatch, revealChatSearchMatch } from "./chat-search-match";

export interface ChatSearchMatch {
  readonly messageId: string;
  readonly term: string;
}

export function useChatSearchMatch({ target, index, container, list, disengage }: {
  readonly target?: ChatSearchMatch;
  readonly index: number;
  readonly container: HTMLElement | null;
  readonly list: RefObject<VirtuosoHandle | null>;
  readonly disengage: () => void;
}) {
  const completed = useRef<ChatSearchMatch | null>(null);
  useEffect(() => {
    if (!target || index < 0 || !container || completed.current === target) return;
    clearChatSearchMatch();
    disengage();
    list.current?.scrollToIndex({ index, align: "start" });
    let frames = 0;
    let frame: number;
    const reveal = () => {
      if (revealChatSearchMatch(container, target.messageId, target.term)) {
        completed.current = target;
        return;
      }
      if (++frames < 60) {
        // History prepend anchoring may supersede the first virtual scroll.
        list.current?.scrollToIndex({ index, align: "start" });
        frame = requestAnimationFrame(reveal);
      }
    };
    frame = requestAnimationFrame(reveal);
    return () => cancelAnimationFrame(frame);
  }, [container, disengage, index, list, target]);
  useEffect(() => {
    return target ? clearChatSearchMatch : undefined;
  }, [target]);
}
