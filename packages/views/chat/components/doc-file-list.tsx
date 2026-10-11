"use client";

import { FileText, Paperclip, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useDocModeStore, selectActiveDocument } from "@multica/core/chat";
import { useChatContextItems } from "./use-chat-context-items";
import { useT } from "../../i18n";
import type { MentionItem } from "../../editor/extensions/mention-suggestion";

// Doc-mode entry point (B, confirmed v12): a file list of the documents the
// chat can open. Only `issue` and `attachment` kinds are openable in doc mode
// — the content-version surface (`DocModeSurface`) renders exactly those two —
// so `project` and other context items are dropped here. Clicking a row calls
// `openDoc`, which fills the detail pane with the document. The list is fed by
// the same context items the composer already tracks (current route + recent),
// so it surfaces the documents the conversation is already about.
export interface DocumentListItem {
  kind: "issue" | "attachment";
  id: string;
  label: string;
}

function itemToDocument(item: MentionItem): DocumentListItem | null {
  if (item.type === "issue") {
    // Prefer the human title; fall back to the identifier (e.g. MUL-123).
    return { kind: "issue", id: item.id, label: item.description?.trim() || item.label };
  }
  if (item.type === "attachment") {
    return { kind: "attachment", id: item.id, label: item.label };
  }
  return null;
}

export function DocModeFileList({ wsId }: { wsId: string }) {
  const { t } = useT("chat");
  const items = useChatContextItems(wsId);
  const openDoc = useDocModeStore((s) => s.openDoc);
  const activeDoc = useDocModeStore(selectActiveDocument(wsId));
  // Local-only filter (v12: the sidebar is files + a filter box). Ephemeral UI
  // state, so it lives here, not in the persisted doc-mode store.
  const [filter, setFilter] = useState("");

  const documents = items
    .map((item) => itemToDocument(item))
    .filter((doc): doc is DocumentListItem => doc !== null);

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return documents;
    return documents.filter((doc) => doc.label.toLowerCase().includes(query));
  }, [documents, filter]);

  if (documents.length === 0) {
    return (
      <div className="p-4 text-muted-foreground">
        <div className="flex items-center gap-2">
          <Search className="h-4 w-4" />
          <p className="text-body">{t(($) => $.doc_file_list.empty)}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b p-2">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t(($) => $.doc_file_list.filter_placeholder)}
            aria-label={t(($) => $.doc_file_list.filter_placeholder)}
            className="w-full rounded-md border border-transparent bg-muted/50 py-1.5 pl-8 pr-2 text-body outline-none transition-colors focus:border-ring focus:bg-background"
          />
        </div>
      </div>
      {visible.length === 0 ? (
        <div className="p-4 text-muted-foreground">
          <p className="text-body">{t(($) => $.doc_file_list.no_match)}</p>
        </div>
      ) : (
        <ul className="flex-1 divide-y overflow-y-auto">
          {visible.map((doc) => {
            const isActive = activeDoc?.id === doc.id && activeDoc?.kind === doc.kind;
            return (
              <li key={`${doc.kind}:${doc.id}`}>
                <button
                  type="button"
                  onClick={() => openDoc(wsId, { ...doc })}
                  aria-current={isActive ? "true" : undefined}
                  className={
                    "flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/50" +
                    (isActive ? " bg-accent/10" : "")
                  }
                >
                  {doc.kind === "issue" ? (
                    <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
                  ) : (
                    <Paperclip className="h-4 w-4 shrink-0 text-muted-foreground" />
                  )}
                  <span className="min-w-0 flex-1 truncate text-body">{doc.label}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
