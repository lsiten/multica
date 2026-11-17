"use client";

import { X } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import type { ChatReference } from "@multica/core/chat";
import { useT } from "../../i18n";

export function ChatReferenceChips({ references, onRemove, onOpen }: {
  readonly references: readonly ChatReference[];
  readonly onRemove?: (id: string) => void;
  readonly onOpen?: (reference: ChatReference) => void;
}) {
  const { t } = useT("chat");
  if (!references.length) return null;
  return (
    <div className="flex max-h-32 shrink-0 flex-wrap items-center gap-1.5 overflow-y-auto px-3 pt-2" aria-label={t(($) => $.workspace.references)}>
      {references.map((reference) => (
        <div key={reference.id} className="inline-flex max-w-full items-center gap-1 rounded-md border bg-surface-raised px-2 py-1 text-caption">
          <button type="button" disabled={(!reference.source && !reference.messageSource) || !onOpen} onClick={() => onOpen?.(reference)} className="min-w-0 truncate text-left hover:underline disabled:no-underline" title={reference.content}>
            {reference.source?.resource.relativePath ?? reference.label}
            {reference.source && <span className="ml-1 text-muted-foreground">
              {reference.source.state === "draft" ? t(($) => $.workspace.reference_draft) : t(($) => $.workspace.reference_saved)}
              {reference.source.revision ? ` · v${reference.source.revision}` : ""}
              {reference.source.extent === "selection" ? ` · ${t(($) => $.workspace.reference_selection)}` : ""}
            </span>}
          </button>
          <IconButton className="shrink-0" label={t(($) => $.workspace.remove_reference, { name: reference.label })} onClick={() => onRemove?.(reference.id)} size="icon-xs" variant="ghost"><X /></IconButton>
        </div>
      ))}
    </div>
  );
}
