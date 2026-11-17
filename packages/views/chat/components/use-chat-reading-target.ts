"use client";

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { VirtuosoHandle } from "react-virtuoso";
import { useChatReadingPositionStore, type ChatReadingPosition } from "@multica/core/chat";
import { chatMessagesPageOptions } from "@multica/core/chat/queries";
import { chatReadingScope, chatScrollerVisible, nearestChatReadingAnchor, type ChatReadingAnchor, type ChatReadingContext, type ChatReadingRow } from "./chat-reading-position";
import type { ChatSearchMatch } from "./use-chat-search-match";

export function useChatReadingTarget(input: {
  readonly context?: ChatReadingContext;
  readonly initial: ChatReadingPosition | undefined;
  readonly rows: readonly ChatReadingRow[];
  readonly container: HTMLElement | null;
  readonly list: RefObject<VirtuosoHandle | null>;
  readonly active: boolean;
  readonly explicitNavigation: ChatSearchMatch | undefined;
  readonly disengage: () => void;
  readonly pin: () => void;
}) {
  const { context, initial, rows, container, list, active, explicitNavigation, disengage, pin } = input;
  const scope = chatReadingScope(context);
  const { data, isFetching, hasNextPage, fetchNextPage } = useInfiniteQuery({ ...chatMessagesPageOptions(context?.sessionId ?? ""), enabled: false });
  const [target, setTarget] = useState<ChatReadingAnchor | null>(() => !explicitNavigation && initial?.kind === "message" ? initial : null);
  const [ready, setReady] = useState(target === null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const generation = useRef(0);
  const fetching = useRef(false);
  const cancel = useCallback(() => { generation.current++; setTarget(null); setReady(true); setFailed(false); }, []);
  const restore = useCallback((position: ChatReadingPosition) => {
    if (position.kind !== "message") return;
    generation.current++;
    disengage();
    setTarget(position); setReady(false); setFailed(false); setAttempt((value) => value + 1);
  }, [disengage]);
  const retry = useCallback(() => { setFailed(false); setReady(false); setAttempt((value) => value + 1); }, []);
  const followLatest = useCallback(() => {
    cancel();
    if (scope) useChatReadingPositionStore.getState().setPosition(scope, { kind: "live" });
    pin();
  }, [cancel, scope, pin]);
  useEffect(() => { if (explicitNavigation) cancel(); }, [explicitNavigation, cancel]);
  useEffect(() => () => { generation.current++; }, []);
  const row = target ? rows.find((entry) => entry.messageId === target.messageId) : undefined;
  const rowIndex = row?.index;

  useEffect(() => {
    if (!scope || !target || row || ready || failed || !active || !container || !chatScrollerVisible(container) || isFetching || fetching.current) return;
    const cached = data?.pages.some((page) => page.messages.some((message) => message.id === target.messageId));
    if (cached) return;
    const oldest = rows[0];
    const passed = oldest && Date.parse(oldest.createdAt) < Date.parse(target.createdAt ?? "");
    if (passed || !hasNextPage) {
      const nearest = nearestChatReadingAnchor(rows, target);
      if (nearest) setTarget(nearest);
      else followLatest();
      return;
    }
    const current = generation.current;
    fetching.current = true;
    void fetchNextPage({ cancelRefetch: false }).then((result) => {
      fetching.current = false;
      if (current !== generation.current) return;
      if (result.isFetchNextPageError) { setFailed(true); setReady(true); }
      else setAttempt((value) => value + 1);
    });
  }, [scope, target, row, ready, failed, active, container, rows, data, isFetching, hasNextPage, fetchNextPage, attempt, followLatest]);

  useEffect(() => {
    const element = container;
    if (!scope || !target || rowIndex === undefined || ready || failed || !active || !element || !chatScrollerVisible(element)) return;
    disengage();
    const current = generation.current;
    list.current?.scrollToIndex({ index: rowIndex, align: "start", offset: target.offset });
    let frame = 0;
    let frames = 0;
    let stable = 0;
    const locate = () => {
      if (current !== generation.current) return;
      const node = [...element.querySelectorAll<HTMLElement>("[data-chat-message-id]")].find((entry) => entry.dataset.chatMessageId === target.messageId);
      if (node && chatScrollerVisible(element)) {
        const correction = target.offset - (element.getBoundingClientRect().top - node.getBoundingClientRect().top);
        const next = Math.max(0, Math.min(element.scrollHeight - element.clientHeight, element.scrollTop + correction));
        if (Math.abs(next - element.scrollTop) <= 1) stable++;
        else { element.scrollTop = next; stable = 0; }
        if (stable >= 2) { setReady(true); return; }
      } else if (chatScrollerVisible(element)) {
        // Prepend anchoring may supersede the first virtual scroll.
        list.current?.scrollToIndex({ index: rowIndex, align: "start", offset: target.offset });
      }
      if (++frames >= 60) { setFailed(true); setReady(true); return; }
      frame = requestAnimationFrame(locate);
    };
    frame = requestAnimationFrame(locate);
    return () => cancelAnimationFrame(frame);
  }, [scope, target, rowIndex, ready, failed, active, container, list, disengage, attempt]);
  return { ready, failed, retry, cancel, restore, followLatest };
}
