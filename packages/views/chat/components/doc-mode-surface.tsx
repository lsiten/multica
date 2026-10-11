"use client";

import { X, GitCompare, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  useDocModeStore,
  selectActiveDocument,
  selectWorking,
  selectDiffView,
} from "@multica/core/chat";
import {
  useDocumentContentVersionCurrent,
  useDocumentContentVersions,
  useRollbackDocumentContentVersion,
} from "@multica/core/chat/document-content-version-queries";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { RichContent } from "../../rich-content";
import { Attachment } from "../../editor/attachment";
import { useT } from "../../i18n";

// A ref with an empty id keeps the content-versions hooks disabled (see their
// `enabled: !!ref.id` guard) so they can be called unconditionally even when no
// document is open — that is what lets the early return below obey the rules
// of hooks.
const EMPTY_DOC = { kind: "issue" as const, id: "" };

/**
 * DocModeSurface — the confirmed v12 "based-on-document" surface.
 *
 * When a document is open (doc-mode) it fills the chat detail pane (C): the
 * document body renders full-area, and a floating layer shows ONLY the working
 * status (D7) — the conversation stays in its original place (D10), it is not
 * moved here. Text (Markdown/HTML) renders via RichContent; a binary document
 * (image / PDF) renders via Attachment and is never edited in place.
 *
 * Default view is the current content with no diff (D6); toggling the diff view
 * shows the version history so the user can compare and roll back (D8/D9).
 * Closing (X) is the only way out — an edit completing never auto-closes (D9).
 */
export function DocModeSurface({ wsId }: { wsId: string }) {
  const { t } = useT("chat");
  const doc = useDocModeStore(selectActiveDocument(wsId));
  const working = useDocModeStore(selectWorking(wsId));
  const diffView = useDocModeStore(selectDiffView(wsId));
  const closeDoc = useDocModeStore((s) => s.closeDoc);
  const setDiffView = useDocModeStore((s) => s.setDiffView);

  // Hooks must run unconditionally; the empty id disables the queries below.
  const activeRef = doc ?? EMPTY_DOC;
  const current = useDocumentContentVersionCurrent(wsId, activeRef);
  const versions = useDocumentContentVersions(wsId, activeRef);
  const rollback = useRollbackDocumentContentVersion(wsId);

  if (!doc) return null;

  const isText = doc.contentType?.startsWith("text/") ?? current.data?.content != null;

  return (
    <div className="flex h-full flex-col min-h-0">
      <div className="flex h-12 shrink-0 items-center justify-between border-b px-3">
        <h2 className="truncate text-body font-semibold">
          {doc.label ?? (doc.kind === "issue" ? t(($) => $.doc_mode.issue) : t(($) => $.doc_mode.attachment))}
        </h2>
        <div className="flex items-center gap-1">
          <Button
            variant={diffView ? "secondary" : "ghost"}
            size="sm"
            className="gap-1.5"
            onClick={() => setDiffView(wsId, !diffView)}
          >
            <GitCompare className="h-4 w-4" />
            {t(($) => $.doc_mode.diff)}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={() => closeDoc(wsId)}
          >
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        {diffView ? (
          <DocVersionHistory
            versions={versions.data ?? []}
            loading={versions.isLoading}
            onRollback={(revision) => rollback.mutate({ ref: doc, revision })}
            rollbackLabel={t(($) => $.doc_mode.rollback)}
          />
        ) : (
          <DocBody
            loading={current.isLoading}
            error={current.isError}
            content={current.data?.content ?? null}
            contentUrl={current.data?.content_url ?? null}
            contentType={doc.contentType ?? current.data?.content_type ?? "text/markdown"}
            isText={isText}
            label={doc.label ?? "Document"}
          />
        )}
      </div>

      <FloatingWorkingIndicator
        working={working}
        label={doc.label ?? "Document"}
        workingLabel={t(($) => $.doc_mode.working)}
      />
    </div>
  );
}

function DocBody({
  loading,
  error,
  content,
  contentUrl,
  contentType,
  isText,
  label,
}: {
  loading: boolean;
  error: boolean;
  content: string | null;
  contentUrl: string | null;
  contentType: string;
  isText: boolean;
  label: string;
}) {
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <p className="text-body">{label}</p>
      </div>
    );
  }
  // Binary (image / PDF): render via the attachment preview; never edited in
  // place (D3/D10). Text (Markdown/HTML): render inline via RichContent.
  if (!isText && contentUrl) {
    return (
      <div className="p-6">
        <Attachment
          attachment={{
            kind: "url",
            url: contentUrl,
            filename: label,
            contentType,
          }}
        />
      </div>
    );
  }
  return (
    <div className="mx-auto max-w-3xl p-6">
      <RichContent content={content ?? ""} className="doc-mode-body" />
    </div>
  );
}

function DocVersionHistory({
  versions,
  loading,
  onRollback,
  rollbackLabel,
}: {
  versions: DocumentContentVersion[];
  loading: boolean;
  onRollback: (revision: number) => void;
  rollbackLabel: string;
}) {
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }
  if (versions.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <p className="text-body">—</p>
      </div>
    );
  }
  return (
    <ul className="divide-y p-4">
      {versions.map((v) => (
        <li key={v.id} className="flex items-center justify-between gap-3 py-3">
          <div className="min-w-0 flex-1">
            <p className="text-body font-medium">v{v.revision}</p>
            {v.note ? (
              <p className="truncate text-muted-foreground">{v.note}</p>
            ) : null}
          </div>
          <Button size="sm" variant="ghost" onClick={() => onRollback(v.revision)}>
            {rollbackLabel}
          </Button>
        </li>
      ))}
    </ul>
  );
}

// D7: the floating layer shows ONLY the working status (spinner) while the
// agent is editing the document — never the conversation content.
function FloatingWorkingIndicator({
  working,
  label,
  workingLabel,
}: {
  working: boolean;
  label: string;
  workingLabel: string;
}) {
  if (!working) return null;
  return (
    <div className="shrink-0 border-t px-4 py-2">
      <div className="flex items-center gap-2 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        <span className="text-body">{workingLabel}</span>
        <span className="text-faint-foreground">· {label}</span>
      </div>
    </div>
  );
}
