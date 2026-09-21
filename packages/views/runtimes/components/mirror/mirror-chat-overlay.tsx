"use client";

import { useQuery } from "@tanstack/react-query";
import { motion, useReducedMotion } from "motion/react";
import { useLayoutEffect, useRef } from "react";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import type { ChatMessage, ChatPendingTask } from "@multica/core/types";
import { isTaskMessageTaskId, taskMessagesOptions } from "@multica/core/chat/queries";
import { buildTimeline } from "../../../common/task-transcript";
import { canonicalAnswerText } from "../../../chat/lib/copy-text";
import { stripChatQuickActionsProtocol } from "../../../chat/lib/quick-actions";
import { useT } from "../../../i18n";

export function MirrorChatOverlay({ messages, pendingTask, agentName }: {
  readonly messages: ChatMessage[];
  readonly pendingTask: ChatPendingTask | null | undefined;
  readonly agentName: string;
}) {
  const { t } = useT("runtimes");
  const reducedMotion = useReducedMotion();
  const scrollRef = useRef<HTMLElement | null>(null);
  const contentRef = useRef<HTMLOListElement | null>(null);
  const following = useRef(true);
  const fade = useScrollFade(scrollRef, 28);
  const pendingId = pendingTask?.task_id;
  const persisted = messages.some((message) => message.role === "assistant" && message.task_id === pendingId);
  const live = useQuery({
    ...taskMessagesOptions(pendingId ?? ""),
    enabled: isTaskMessageTaskId(pendingId) && !persisted,
  });
  const rows = messages
    .filter((message) => message.message_kind !== "onboarding_kickoff")
    .map((message) => ({
      id: message.role === "assistant" && message.task_id ? `task:${message.task_id}` : message.id,
      author: message.role === "assistant" ? agentName : t(($) => $.vscreen.chat_you),
      text: canonicalAnswerText(message).trim(),
    }))
    .filter((row) => row.text);
  if (pendingId && !persisted) {
    const text = stripChatQuickActionsProtocol(buildTimeline(live.data ?? [])
      .filter((item) => item.type === "text")
      .map((item) => item.content ?? "")
      .join("\n")).trim();
    if (text) rows.push({ id: `task:${pendingId}`, author: agentName, text });
  }

  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    if (scroller && following.current) scroller.scrollTop = scroller.scrollHeight;
  }, [messages, live.data]);
  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    const content = contentRef.current;
    if (!scroller || !content) return;
    const observer = new ResizeObserver(() => {
      if (following.current) scroller.scrollTop = scroller.scrollHeight;
    });
    observer.observe(scroller);
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  return (
    <aside
      ref={scrollRef}
      tabIndex={0}
      aria-label={t(($) => $.vscreen.chat_overlay)}
      className="dark absolute bottom-3 right-3 h-[34%] max-h-44 w-[min(22rem,65%)] overflow-y-auto overscroll-contain text-foreground [scrollbar-width:none] [&::-webkit-scrollbar]:hidden focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
      style={fade}
      onWheel={(event) => {
        event.stopPropagation();
        if (event.deltaY < 0) following.current = false;
      }}
      onTouchStart={(event) => { event.stopPropagation(); following.current = false; }}
      onKeyDown={(event) => {
        event.stopPropagation();
        if (["ArrowUp", "PageUp", "Home"].includes(event.key)) following.current = false;
      }}
      onScroll={(event) => {
        const scroller = event.currentTarget;
        following.current = scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 8;
      }}
    >
      <motion.ol ref={contentRef} initial={false} className="flex min-h-full flex-col items-start justify-end gap-1.5" aria-live="polite" aria-relevant="additions text">
        {rows.map((row) => (
          <motion.li
            key={row.id}
            initial={reducedMotion ? false : { opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: reducedMotion ? 0 : 0.18, ease: "easeOut" }}
            className="max-w-full shrink-0 rounded-lg bg-background/65 px-2.5 py-1 text-caption leading-relaxed"
          >
            <p className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">
              <span className="font-medium text-info">{row.author}: </span>{row.text}
            </p>
          </motion.li>
        ))}
      </motion.ol>
    </aside>
  );
}
