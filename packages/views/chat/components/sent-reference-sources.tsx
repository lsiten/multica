"use client";

import { readSentDocumentReferences, readSentMessageReferences, type SentDocumentReference, type SentMessageReference } from "@multica/core/chat";
import { File, MessageSquare } from "@multica/ui/icons";
import { useT } from "../../i18n";

export function SentReferenceSources({ content, workspaceId, onOpen, onOpenMessage }: {
  readonly content: string;
  readonly workspaceId?: string;
  readonly onOpen?: (reference: SentDocumentReference) => void;
  readonly onOpenMessage?: (reference: SentMessageReference) => void;
}) {
  const { t } = useT("chat");
  const references = readSentDocumentReferences(content);
  const messages = readSentMessageReferences(content);
  if (!references.length && !messages.length) return null;
  return <div className="mb-2 flex flex-wrap gap-1.5" aria-label={t(($) => $.workspace.references)}>
    {references.map((reference, index) => <button key={index} type="button"
      disabled={!onOpen || reference.workspaceId !== workspaceId}
      title={t(($) => $.workspace.open_reference_source)}
      onClick={() => onOpen?.(reference)}
      className="inline-flex max-w-full items-center gap-1 rounded-md border bg-surface-raised px-2 py-1 text-caption text-left hover:bg-accent disabled:opacity-60">
      <File className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="truncate">{reference.label}</span>
      {" "}
      <span className="shrink-0 text-muted-foreground">{reference.state === "draft" ? t(($) => $.workspace.reference_draft) : t(($) => $.workspace.reference_saved)}{reference.revision ? ` · v${reference.revision}` : ""}</span>
    </button>)}
    {messages.map((reference, index) => <button key={`message:${index}`} type="button"
      disabled={!onOpenMessage || reference.workspaceId !== workspaceId}
      onClick={() => onOpenMessage?.(reference)}
      className="inline-flex max-w-full items-center gap-1 rounded-md border bg-surface-raised px-2 py-1 text-left text-caption hover:bg-accent disabled:opacity-60">
      <MessageSquare className="size-3.5 shrink-0" aria-hidden="true" /><span className="truncate">{reference.label}</span>
    </button>)}
  </div>;
}
