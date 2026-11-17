"use client";

import { useState, type DragEvent, type KeyboardEvent } from "react";
import { File, MessageSquare, X } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { cn } from "@multica/ui/lib/utils";
import { useDocModeStore, documentDraftKey, useChatWorkspaceLayoutStore, DOCUMENT_TAB_MIME, documentTabDragValue, type DocumentRef } from "@multica/core/chat";
import { useT } from "../../i18n";

const EMPTY_DOCUMENTS: readonly DocumentRef[] = [];

export function ChatResourceTabs({ sessionId, scope }: {
  readonly sessionId: string;
  readonly scope: string;
}) {
  const { t } = useT("chat");
  const opened = useDocModeStore((state) => state.openBySession[sessionId] ?? EMPTY_DOCUMENTS);
  const active = useDocModeStore((state) => state.activeBySession[sessionId] ?? null);
  const openDoc = useDocModeStore((state) => state.openDoc);
  const selectChat = useDocModeStore((state) => state.selectChat);
  const closeTab = useDocModeStore((state) => state.closeTab);
  const moveTab = useDocModeStore((state) => state.moveTab);
  const setLayout = useChatWorkspaceLayoutStore((state) => state.setLayout);
  const [dropActive, setDropActive] = useState(false);
  const documents = opened.length > 0 ? opened : active ? [active] : EMPTY_DOCUMENTS;
  const selectDocument = (doc: DocumentRef) => {
    openDoc(sessionId, doc);
    setLayout(scope, { mode: "focused" });
  };
  const dropDocument = (event: DragEvent, target?: string) => {
    event.preventDefault();
    setDropActive(false);
    const payload = event.dataTransfer.getData(DOCUMENT_TAB_MIME);
    const doc = documents.find((candidate) => payload === documentTabDragValue(sessionId, candidate));
    if (!doc) return;
    if (target) moveTab(sessionId, documentDraftKey(doc), target);
    selectDocument(doc);
  };
  const navigateTabs = (event: KeyboardEvent<HTMLDivElement>) => {
    const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>("[role=tab]"));
    const index = tabs.findIndex((tab) => tab === event.target);
    if (index < 0) return;
    let next: number;
    switch (event.key) {
      case "ArrowRight": next = (index + 1) % tabs.length; break;
      case "ArrowLeft": next = (index - 1 + tabs.length) % tabs.length; break;
      case "Home": next = 0; break;
      case "End": next = tabs.length - 1; break;
      default: return;
    }
    event.preventDefault();
    tabs[next]?.focus();
    tabs[next]?.click();
  };

  return (
    <div role="tablist" aria-label={t(($) => $.workspace.panes)} onKeyDown={navigateTabs}
      className={cn("flex min-w-0 flex-1 items-center gap-1 overflow-x-auto", dropActive && "bg-accent")}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes(DOCUMENT_TAB_MIME)) return;
        event.preventDefault(); setDropActive(true);
      }} onDragLeave={() => setDropActive(false)} onDrop={(event) => dropDocument(event)}>
      <button type="button" role="tab" aria-selected={!active} tabIndex={active ? -1 : 0}
        onClick={() => selectChat(sessionId)}
        className={cn("flex h-8 shrink-0 items-center gap-2 rounded-md px-3 text-label outline-none focus-visible:ring-2 focus-visible:ring-ring", !active ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/60")}>
        <MessageSquare className="size-4" aria-hidden="true" />{t(($) => $.workspace.conversation)}
      </button>
      {documents.map((doc) => {
        const key = documentDraftKey(doc);
        const selected = !!active && documentDraftKey(active) === key;
        const name = doc.label ?? doc.relativePath ?? t(($) => $.workspace.document);
        return (
          <div key={key} className={cn("flex min-w-0 shrink-0 items-center rounded-md", selected && "bg-accent")}
            onDragOver={(event) => { if (event.dataTransfer.types.includes(DOCUMENT_TAB_MIME)) event.preventDefault(); }}
            onDrop={(event) => { event.stopPropagation(); dropDocument(event, key); }}>
            <button type="button" role="tab" aria-selected={selected} tabIndex={selected ? 0 : -1} draggable
              title={name} onClick={() => selectDocument(doc)}
              onDragStart={(event) => {
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData(DOCUMENT_TAB_MIME, documentTabDragValue(sessionId, doc));
                event.dataTransfer.setData("text/plain", name);
              }}
              className="flex h-8 min-w-0 max-w-64 items-center gap-2 rounded-md px-3 text-label outline-none hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring">
              <File className="size-4 shrink-0" aria-hidden="true" /><span className="truncate">{name}</span>
            </button>
            <IconButton label={t(($) => $.workspace.close_tab, { name })} size="icon-xs" onClick={() => closeTab(sessionId, key)}><X /></IconButton>
          </div>
        );
      })}
    </div>
  );
}
