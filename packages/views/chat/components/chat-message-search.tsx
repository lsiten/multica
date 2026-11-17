"use client";

import { useEffect, useId, useState } from "react";
import { Search, Loader2 } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { Input } from "@multica/ui/components/ui/input";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@multica/ui/components/ui/sheet";
import { useChatMessageSearch } from "@multica/core/chat";
import type { ChatSession } from "@multica/core/types";
import { useT } from "../../i18n";

export function ChatMessageSearch({ wsId, sessions, activeSessionId, onSelect }: {
  readonly wsId: string;
  readonly sessions: readonly ChatSession[];
  readonly activeSessionId: string | null;
  readonly onSelect: (session: ChatSession, messageId: string, term: string) => void;
}) {
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState("");
  const [debounced, setDebounced] = useState("");
  const [all, setAll] = useState(false);
  const scopeId = useId();
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(term), 250);
    return () => clearTimeout(timer);
  }, [term]);
  const candidates = all ? sessions : sessions.filter((session) => session.id === activeSessionId);
  const query = useChatMessageSearch(wsId, candidates, debounced, open);
  const busy = query.isFetching || term !== debounced;
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger render={<IconButton label={t(($) => $.workspace.search_messages)} />}><Search /></SheetTrigger>
      <SheetContent className="flex min-w-0 flex-col gap-3">
        <SheetHeader><SheetTitle>{t(($) => $.workspace.search_messages)}</SheetTitle></SheetHeader>
        <div className="space-y-3 px-4">
          <Input value={term} onChange={(event) => setTerm(event.target.value)} aria-label={t(($) => $.workspace.search_messages)} autoFocus />
          <label htmlFor={scopeId} className="flex items-center gap-2 text-body"><Checkbox id={scopeId} checked={all} onCheckedChange={setAll} />{t(($) => $.workspace.search_all_chats)}</label>
          <p role="status" className="text-caption text-muted-foreground">{t(($) => $.workspace.messages_scanned, { count: query.scanned })}{busy && <Loader2 className="ml-2 inline size-3 animate-spin" />}</p>
        </div>
        <div className="min-h-0 flex-1 space-y-1 overflow-y-auto px-3" aria-busy={busy}>
          {!busy && !query.isError && debounced.trim() && query.matches.length === 0 && <p className="px-2 text-body">{t(($) => $.workspace.no_message_matches)}</p>}
          {query.isError && <p role="alert" className="px-2 text-body text-destructive">{t(($) => $.doc_file_list.search_failed)}</p>}
          {query.matches.map((match) => (
            <button key={match.messageId} type="button" className="w-full space-y-1 rounded-md p-2 text-left hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { onSelect(match.session, match.messageId, debounced.trim()); setOpen(false); }}>
              <span className="block truncate text-caption font-medium">{match.session.title}</span>
              <span className="block whitespace-pre-wrap break-words text-body">{match.excerpt}</span>
            </button>
          ))}
        </div>
        <div className="px-4 pb-4">
          {query.isError ? <Button variant="outline" onClick={() => void query.refetch()}>{t(($) => $.doc_mode.retry)}</Button>
            : query.hasNextPage && <Button variant="outline" disabled={busy} onClick={() => void query.fetchNextPage()}>{t(($) => $.workspace.search_older_messages)}</Button>}
        </div>
      </SheetContent>
    </Sheet>
  );
}
