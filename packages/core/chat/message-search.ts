"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { ChatSession } from "../types";

interface SearchCursor {
  readonly sessionIndex: number;
  readonly before: { created_at: string; id: string } | null;
}
const INITIAL_SEARCH_CURSOR: SearchCursor = { sessionIndex: 0, before: null };

/** Reads one authorized history page per step and reports its actual coverage. */
export function useChatMessageSearch(wsId: string, sessions: readonly ChatSession[], term: string, enabled: boolean) {
  const normalized = term.trim().toLocaleLowerCase();
  const search = useInfiniteQuery({
    queryKey: ["chat", wsId, "message-search", sessions.map((session) => session.id), normalized],
    initialPageParam: INITIAL_SEARCH_CURSOR,
    queryFn: async ({ pageParam, signal }) => {
      const session = sessions[pageParam.sessionIndex];
      if (!session) return { matches: [], scanned: 0, next: undefined };
      signal.throwIfAborted();
      const page = await api.listChatMessagesPage(session.id, { before: pageParam.before, limit: 100 });
      signal.throwIfAborted();
      const next: SearchCursor | undefined = page.has_more && page.next_cursor
        ? { sessionIndex: pageParam.sessionIndex, before: page.next_cursor }
        : pageParam.sessionIndex + 1 < sessions.length ? { sessionIndex: pageParam.sessionIndex + 1, before: null } : undefined;
      return {
        matches: page.messages.flatMap((message) => {
          const position = message.content.toLocaleLowerCase().indexOf(normalized);
          if (position < 0) return [];
          const start = Math.max(0, position - 70);
          return [{ session, messageId: message.id, excerpt: message.content.slice(start, position + normalized.length + 130), createdAt: message.created_at }];
        }),
        scanned: page.messages.length,
        next,
      };
    },
    getNextPageParam: (page) => page.next,
    enabled: enabled && !!normalized && sessions.length > 0,
    retry: false,
    staleTime: 30_000,
  });
  return {
    ...search,
    matches: search.data?.pages.flatMap((page) => page.matches) ?? [],
    scanned: search.data?.pages.reduce((total, page) => total + page.scanned, 0) ?? 0,
  };
}
