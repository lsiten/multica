"use client";

import { GripVertical } from "@multica/ui/icons";
import { DOCUMENT_TAB_MIME, documentTabDragValue, useChatWorkspaceLayoutStore, type DocumentRef } from "@multica/core/chat";
import { useT } from "../../i18n";

/** The title is both a drag source and a keyboard alternative to docking. */
export function DocumentDockTitle({ document, wsId, sessionId }: {
  readonly document: DocumentRef;
  readonly wsId: string;
  readonly sessionId: string;
}) {
  const { t } = useT("chat");
  const setLayout = useChatWorkspaceLayoutStore((state) => state.setLayout);
  const name = document.relativePath ?? document.label ?? t(($) => $.workspace.document);
  return (
    <h2 className="min-w-0 text-body font-semibold">
      <button type="button" draggable title={name} aria-label={t(($) => $.workspace.open_full_tab, { name })}
        onClick={() => setLayout(JSON.stringify([wsId, sessionId]), { mode: "focused" })}
        onDragStart={(event) => {
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData(DOCUMENT_TAB_MIME, documentTabDragValue(sessionId, document));
          event.dataTransfer.setData("text/plain", name);
        }}
        className="flex min-h-8 max-w-full cursor-grab items-center gap-1 rounded-md px-1 text-left outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring active:cursor-grabbing">
        <GripVertical className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{name}</span>
      </button>
    </h2>
  );
}
