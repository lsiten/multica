"use client";

import { X, GitCompare, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import {
  useDocModeStore,
  selectActiveDocument,
  selectWorking,
  selectDiffView,
} from "@multica/core/chat";
import { chatSessionFilesOptions } from "@multica/core/chat/queries";
import {
  useDocumentContentVersionCurrent,
  useDocumentContentVersions,
  useRollbackDocumentContentVersion,
} from "@multica/core/chat/document-content-version-queries";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { RichContent } from "../../rich-content";
import { Attachment } from "../../editor/attachment";
import { useT } from "../../i18n";
import { requestReviewPage } from "../../platform/local-review-pages";

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
  const { data: sessionFiles } = useQuery({
    ...chatSessionFilesOptions(wsId, doc?.sessionId ?? "", doc?.projectId),
  });
  const workspaceTask = doc?.kind === "workspace_file"
    ? [...(sessionFiles?.project_tasks ?? []), ...(sessionFiles?.tasks ?? [])].find((task) => task.id === doc.taskId && task.work_dir)
    : undefined;
  const workspaceContent = useQuery({
    queryKey: ["chat-workspace-file-content", wsId, doc?.id, doc?.relativePath],
    queryFn: () => requestReviewPage({
      task_id: workspaceTask!.id,
      workspace_id: wsId,
      runtime_id: workspaceTask!.runtime_id,
      path: workspaceTask!.work_dir!,
      action: "file_content",
      relative_path: doc!.relativePath!,
      offset: 0,
      limit: 256 * 1024,
    }),
    enabled: doc?.kind === "workspace_file" && !!workspaceTask && !!doc.relativePath,
    staleTime: 15_000,
  });

  if (!doc) return null;

  if (doc.kind === "workspace_file") {
    // The content query only fetches the first page; a truncated file reports
    // has_more, so surface the full size instead of silently cutting the text.
    const fileContent =
      workspaceContent.data && "content" in workspaceContent.data
        ? workspaceContent.data.content
        : null;
    return (
      <div className="flex h-full flex-col min-h-0">
        <div className="flex h-12 shrink-0 items-center justify-between border-b px-3">
          <h2 className="truncate text-body font-semibold">{doc.relativePath ?? doc.label ?? "File"}</h2>
          <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => closeDoc(wsId)}>
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
        <WorkspaceFileBody
          loading={workspaceContent.isLoading || (!!doc.taskId && !sessionFiles)}
          error={workspaceContent.isError || !workspaceTask}
          content={fileContent?.text ?? null}
          truncated={fileContent?.has_more ?? false}
          total={fileContent?.size ?? 0}
          label={doc.relativePath ?? doc.label ?? "File"}
        />
      </div>
    );
  }

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

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${Math.round(bytes / 1024)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

function WorkspaceFileBody({
  loading,
  error,
  content,
  truncated,
  total,
  label,
}: {
  loading: boolean;
  error: boolean;
  content: string | null;
  truncated: boolean;
  total: number;
  label: string;
}) {
  const { t } = useT("chat");
  if (loading) {
    return <div className="flex h-full items-center justify-center text-muted-foreground"><Loader2 className="h-5 w-5 animate-spin" /></div>;
  }
  if (error || content === null) {
    return <div className="flex h-full items-center justify-center text-muted-foreground"><p className="text-body">{label}</p></div>;
  }
  return (
    <div className="flex h-full flex-col min-h-0">
      <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap p-6 font-mono text-caption">{content}</pre>
      {truncated && (
        <p className="border-t px-6 py-2 text-caption text-muted-foreground">
          {t(($) => $.doc_mode.truncated, { total: formatBytes(total) })}
        </p>
      )}
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
