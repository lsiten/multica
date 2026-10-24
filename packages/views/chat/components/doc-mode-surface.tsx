"use client";

import { X, GitCompare, Loader2, Pencil, MessageSquareQuote, Code2, Eye, FilePlus } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { autoUpdate, computePosition, flip, hide, offset, shift } from "@floating-ui/dom";
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
  useRecordDocumentContentVersion,
  useRollbackDocumentContentVersion,
} from "@multica/core/chat/document-content-version-queries";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { RichContent } from "../../rich-content";
import { Attachment } from "../../editor/attachment";
import { useT } from "../../i18n";
import { applyReviewFileEdit, requestReviewPage } from "../../platform/local-review-pages";
import { canOpenLocalPath, openLocalPath } from "../../platform";
import { createSafeId } from "@multica/core/utils";
import { api, ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { toast } from "sonner";
import { FileViewer } from "../../skills/components/file-viewer";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { CodeBlockStatic } from "../../editor/code-block-static";
import { ContentEditor } from "../../editor";
import { languageForPath } from "../../common/task-transcript/diff-highlight";
import type { Attachment as AttachmentRecord } from "@multica/core/types";

// A ref with an empty id keeps the content-versions hooks disabled (see their
// `enabled: !!ref.id` guard) so they can be called unconditionally even when no
// document is open — that is what lets the early return below obey the rules
// of hooks.
const EMPTY_DOC = { kind: "issue" as const, id: "" };

function fileContentText(value: unknown): string | null {
  if (!value || typeof value !== "object" || !("content" in value)) return null;
  const content = value.content;
  if (!content || typeof content !== "object" || !("text" in content) || typeof content.text !== "string") return null;
  return content.text;
}

function textCodeBlock(content: string): string {
  return ["```text", content, "```"].join("\n");
}

function documentQuote(label: string, content: string): string {
  return [`> 引用文件：${label}`, "", textCodeBlock(content)].join("\n");
}

function isOfficePath(path: string): boolean {
  return /\.(docx?|xlsx?|pptx?|odt|ods|odp|pages|numbers|key)$/i.test(path);
}

function localFilePath(root: string, relativePath: string): string | null {
  const normalized = relativePath.replaceAll("\\", "/").replace(/^\.\//, "");
  if (!normalized || normalized.startsWith("/") || normalized.split("/").some((part) => part === "..")) return null;
  return `${root.replace(/[\\/]+$/, "")}/${normalized}`;
}

/**
 * useDocSelectionBubble — a floating selection toolbar for the read-only
 * document surfaces (DocBody / WorkspaceFileBody).
 *
 * It mirrors issue-detail's EditorBubbleMenu behaviour for the read-only
 * RichContent / CodeBlock renderers (which have no Tiptap Editor to couple to):
 * a selection inside the container raises a floating toolbar above it,
 * positioned with @floating-ui/dom + a portal to document.body so it escapes
 * the scroll container. The actions reuse issue-detail's intent:
 *   - "Add sub-issue" -> useCreateIssue({ title, parent_issue_id }). Read-only:
 *     the selection is never rewritten, only a sub-issue is created under the
 *     in-scope parent issue (the same CreateSubIssueButton action as issue-detail).
 *   - "Add to chat"   -> the existing onAddToChat reference flow. The composer
 *     then shows the quoted selection, which can be commented on / changed — the
 *     "add to conversation" flow that replaces issue-detail's "add comment".
 */
export function useDocSelectionBubble({
  containerRef,
  parentIssueId,
  onAddToChat,
}: {
  containerRef: RefObject<HTMLDivElement | null>;
  parentIssueId?: string;
  onAddToChat?: (content: string) => void;
}) {
  const { t } = useT("chat");
  const [range, setRange] = useState<Range | null>(null);
  const bubbleRef = useRef<HTMLDivElement>(null);
  const hasActions = !!parentIssueId || !!onAddToChat;

  // Read a live, meaningful text selection that lives inside our container.
  const detect = useCallback(() => {
    if (!hasActions) return;
    const container = containerRef.current;
    if (!container) return;
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) {
      setRange(null);
      return;
    }
    const candidate = selection.getRangeAt(0);
    const inside =
      candidate.startContainer instanceof Node &&
      candidate.endContainer instanceof Node &&
      container.contains(candidate.startContainer) &&
      container.contains(candidate.endContainer);
    // A selection outside this container belongs to another surface; leave ours
    // untouched instead of clearing it.
    if (!inside) return;
    if (selection.isCollapsed || !selection.toString().trim()) {
      setRange(null);
      return;
    }
    setRange(candidate);
  }, [containerRef, hasActions]);

  // Re-detect on pointer/keyboard selection anywhere in the document.
  useEffect(() => {
    document.addEventListener("mouseup", detect);
    document.addEventListener("keyup", detect);
    return () => {
      document.removeEventListener("mouseup", detect);
      document.removeEventListener("keyup", detect);
    };
  }, [detect]);

  // Close when the user clicks anywhere outside the toolbar and the content.
  useEffect(() => {
    if (!range) return;
    const handle = (event: MouseEvent) => {
      const target = event.target as Node;
      if (bubbleRef.current?.contains(target)) return;
      if (containerRef.current?.contains(target)) return;
      setRange(null);
    };
    document.addEventListener("mousedown", handle);
    return () => document.removeEventListener("mousedown", handle);
  }, [range, containerRef]);

  // Anchor the portal above the selection; autoUpdate keeps it pinned while the
  // content scrolls (mirrors EditorBubbleMenu / CommentSelectionBubble).
  useEffect(() => {
    if (!range) return;
    const element = bubbleRef.current;
    const container = containerRef.current;
    if (!element || !container) return;
    let active = true;
    const anchor = {
      contextElement: container,
      getBoundingClientRect: () => range.getBoundingClientRect(),
    };
    const update = () => {
      computePosition(anchor, element, {
        strategy: "fixed",
        placement: "top",
        middleware: [
          offset(8),
          flip({ padding: 8, altBoundary: true }),
          shift({ padding: 8, altBoundary: true }),
          hide(),
        ],
      }).then(({ x, y, middlewareData }) => {
        if (!active || !element.isConnected) return;
        const rect = range.getBoundingClientRect();
        const hidden =
          !range.startContainer.isConnected ||
          !container.isConnected ||
          (rect.width === 0 && rect.height === 0) ||
          middlewareData.hide?.referenceHidden;
        element.style.visibility = hidden ? "hidden" : "visible";
        element.style.left = `${x}px`;
        element.style.top = `${y}px`;
      });
    };
    const cleanup = autoUpdate(anchor, element, update);
    update();
    return () => {
      active = false;
      cleanup();
    };
  }, [range, containerRef]);

  // Drop the native selection so a later mouseup cannot re-open the toolbar.
  const clear = useCallback(() => {
    window.getSelection()?.removeAllRanges();
    setRange(null);
  }, []);

  const onAddToConversation = useCallback(() => {
    if (!range) return;
    const text = range.toString();
    onAddToChat?.(text);
    clear();
  }, [range, onAddToChat, clear]);

  if (!range) return { bubble: null };

  return {
    bubble: createPortal(
      <div
        ref={bubbleRef}
        onMouseDown={(e) => e.preventDefault()}
        className="doc-selection-bubble"
        style={{ position: "fixed", zIndex: 50, width: "max-content", visibility: "hidden" }}
      >
        <div className="flex items-center gap-1 rounded-lg border border-border bg-popover p-1 shadow-md">
          {parentIssueId && (
            <DocSubIssueButton range={range} parentIssueId={parentIssueId} clear={clear} />
          )}
          {onAddToChat && (
            <Button size="sm" variant="ghost" className="gap-1.5" onClick={onAddToConversation}>
              <MessageSquareQuote className="h-3.5 w-3.5" />
              {t(($) => $.doc_mode.add_to_chat)}
            </Button>
          )}
        </div>
      </div>,
      document.body,
    ),
  };
}

/**
 * DocSubIssueButton — the "add sub-issue" action of the selection toolbar.
 *
 * Only mounts when a parent issue is in scope, so useCreateIssue() (which
 * resolves the workspace id via useWorkspaceId) is never called for surfaces
 * without a parent — e.g. workspace files — or in a workspace-less test
 * tree. Kept out of useDocSelectionBubble so that hook can run anywhere.
 */
function DocSubIssueButton({
  range,
  parentIssueId,
  clear,
}: {
  range: Range;
  parentIssueId: string;
  clear: () => void;
}) {
  const { t } = useT("chat");
  const createIssue = useCreateIssue();
  const [pending, setPending] = useState(false);
  const handleClick = async () => {
    if (pending) return;
    // Title from the selection: collapse whitespace, cap length (issue-detail's
    // CreateSubIssueButton caps the same way). Read-only, so we never rewrite it.
    const title = range.toString().replace(/\s+/g, " ").trim().slice(0, 200);
    if (!title) return;
    setPending(true);
    try {
      const created = await createIssue.mutateAsync({ title, parent_issue_id: parentIssueId });
      toast.success(t(($) => $.doc_mode.sub_issue_created, { identifier: created.identifier }));
      clear();
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : t(($) => $.doc_mode.sub_issue_failed),
      );
    } finally {
      setPending(false);
    }
  };
  return (
    <Button size="sm" variant="ghost" className="gap-1.5" onClick={handleClick} disabled={pending}>
      {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FilePlus className="h-3.5 w-3.5" />}
      {t(($) => $.doc_mode.add_sub_issue)}
    </Button>
  );
}

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
export function DocModeSurface({ wsId, sessionId, onAddToChat }: { wsId: string; sessionId: string; onAddToChat?: (content: string) => void }) {
  const { t } = useT("chat");
  const doc = useDocModeStore(selectActiveDocument(sessionId));
  const working = useDocModeStore(selectWorking(sessionId));
  const diffView = useDocModeStore(selectDiffView(sessionId));
  const closeDoc = useDocModeStore((s) => s.closeDoc);
  const setDiffView = useDocModeStore((s) => s.setDiffView);
  const record = useRecordDocumentContentVersion(wsId);
  const user = useAuthStore((s) => s.user);
  const [editing, setEditing] = useState(false);
  const [edited, setEdited] = useState("");
  const [saveError, setSaveError] = useState<string | null>(null);
  const [workspaceEditing, setWorkspaceEditing] = useState(false);
  const [workspaceEdited, setWorkspaceEdited] = useState("");
  const [workspaceSaveError, setWorkspaceSaveError] = useState<string | null>(null);
  const [workspaceSaving, setWorkspaceSaving] = useState(false);

  const documentIdentity = `${doc?.sessionId ?? sessionId}:${doc?.kind ?? "none"}:${doc?.id ?? ""}:${doc?.taskId ?? ""}:${doc?.relativePath ?? ""}`;
  useEffect(() => {
    // The surface is reused across file/session switches. Local editor state
    // must never survive onto the next document while its query is resolving.
    setEditing(false);
    setEdited("");
    setSaveError(null);
    setWorkspaceEditing(false);
    setWorkspaceEdited("");
    setWorkspaceSaveError(null);
  }, [documentIdentity]);

  // Hooks must run unconditionally; the empty id disables the queries below.
  const activeRef = doc ?? EMPTY_DOC;
  const isTextAttachment = doc?.kind === "attachment" && (
    doc.contentType?.startsWith("text/") ||
    /\.(md|mdx|txt|json|ya?ml|csv|log|py|ts|tsx|js|jsx|go|rs|java|css|html)$/i.test(doc.label ?? "")
  );
  const current = useDocumentContentVersionCurrent(wsId, activeRef, { loadAttachments: !isTextAttachment || diffView });
  const versions = useDocumentContentVersions(wsId, activeRef, { loadAttachments: diffView });
  const attachmentTextContent = useQuery({
    queryKey: ["attachment-text-content", doc?.id],
    queryFn: ({ signal }) => api.getAttachmentTextContent(doc!.id, signal),
    enabled: isTextAttachment && !diffView,
    staleTime: 60_000,
    retry: false,
  });
  const rollback = useRollbackDocumentContentVersion(wsId);
  const { data: sessionFiles } = useQuery({
    ...chatSessionFilesOptions(wsId, doc?.sessionId ?? "", doc?.projectId, doc?.kind === "attachment"),
  });
  const attachmentRecord = doc?.kind === "attachment"
    ? [...(sessionFiles?.attachments ?? []), ...(sessionFiles?.project_attachments ?? [])].find(
        (attachment) => attachment.id === doc.id,
      )
    : undefined;
  const workspaceTask = doc?.kind === "workspace_file"
    ? [...(sessionFiles?.project_tasks ?? []), ...(sessionFiles?.tasks ?? [])].find((task) => task.id === doc.taskId && task.work_dir)
    : undefined;
  const projectDirectory = doc?.kind === "workspace_file" && doc.projectResourceId
    ? sessionFiles?.directories?.find((directory) => directory.project_resource_id === doc.projectResourceId)
    : undefined;
  const workspacePath = projectDirectory?.path ?? workspaceTask?.work_dir ?? doc?.workDir;
  const workspaceRuntimeId = projectDirectory?.runtime_id ?? workspaceTask?.runtime_id ?? doc?.runtimeId;
  const workspaceRequest = useMemo(() =>
    doc?.kind === "workspace_file" &&
    workspacePath &&
    workspaceRuntimeId &&
    (doc.taskId || doc.projectResourceId) &&
    doc.relativePath
      ? {
          task_id: doc.taskId ?? "",
          project_resource_id: doc.projectResourceId,
          workspace_id: wsId,
          runtime_id: workspaceRuntimeId,
          path: workspacePath,
          action: "file_content" as const,
          relative_path: doc.relativePath,
          offset: 0,
          // Keep the client request within the API relay's bounded page size.
          // The daemon can read larger pages locally, but a disconnected or
          // older desktop falls back to /api/local-reviews/execute, whose
          // contract caps file_content pages at 64 KiB.
          limit: 64 * 1024,
        }
      : null,
    [doc?.kind, doc?.taskId, doc?.projectResourceId, doc?.relativePath, workspacePath, workspaceRuntimeId, wsId],
  );
  const workspaceContent = useQuery({
    queryKey: [
      "chat-workspace-file-content",
      wsId,
      doc?.sessionId,
      doc?.id,
      doc?.taskId,
      doc?.projectResourceId,
      workspaceRuntimeId,
      workspacePath,
      doc?.relativePath,
    ],
    queryFn: ({ signal }) =>
      workspaceRequest
        ? requestReviewPage(workspaceRequest, signal)
        : Promise.reject(new Error("workspace file reference is incomplete")),
    enabled: !!workspaceRequest,
    staleTime: 60_000,
  });
  const workspaceContentText =
    workspaceRequest &&
    !workspaceContent.isFetching &&
    workspaceContent.data &&
    "path" in workspaceContent.data &&
    workspaceContent.data.path === workspaceRequest.relative_path
      ? fileContentText(workspaceContent.data)
      : null;

  const openWorkspaceEdit = useCallback(() => {
    if (workspaceContentText === null) return;
    setWorkspaceEdited(workspaceContentText);
    setWorkspaceSaveError(null);
    setWorkspaceEditing(true);
  }, [workspaceContentText]);

  const saveWorkspaceEdit = useCallback(async () => {
    if (!doc || doc.kind !== "workspace_file" || !workspacePath || !workspaceRuntimeId || !workspaceRequest) return;
    setWorkspaceSaving(true);
    setWorkspaceSaveError(null);
    try {
      const result = await applyReviewFileEdit({
        task_id: workspaceRequest.task_id,
        workspace_id: workspaceRequest.workspace_id,
        runtime_id: workspaceRequest.runtime_id,
        path: workspaceRequest.path,
        target: "HEAD",
        relative_path: workspaceRequest.relative_path,
        command_id: createSafeId(),
        action: "merge_edit",
        edit_base: workspaceContentText ?? "",
        edit_new: workspaceEdited,
      });
      if (result?.conflict) {
        setWorkspaceSaveError("changed_reload");
        await workspaceContent.refetch();
        return;
      }
      setWorkspaceEditing(false);
      await workspaceContent.refetch();
    } catch {
      setWorkspaceSaveError("save_failed");
    } finally {
      setWorkspaceSaving(false);
    }
  }, [doc, workspacePath, workspaceRequest, workspaceRuntimeId, workspaceContent, workspaceContentText, workspaceEdited]);

  const openEdit = useCallback(() => {
    if (!current.data?.content) return;
    setEdited(current.data.content);
    setSaveError(null);
    setEditing(true);
    setDiffView(sessionId, false);
  }, [current.data?.content, setDiffView, sessionId]);

  const save = useCallback(async () => {
    if (!doc || !current.data) return;
    try {
      await record.mutateAsync({
        ref: doc,
        input: {
          content_type: current.data.content_type ?? doc.contentType ?? "text/markdown",
          content: edited,
          expected_revision: current.data.revision,
        },
      });
      setEditing(false);
      setSaveError(null);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        current.refetch();
        setSaveError("changed_reload");
      } else {
        setSaveError("save_failed");
      }
    }
  }, [record, doc, edited, current]);

  if (!doc) return null;

  if (doc.kind === "workspace_file") {
    // The content query only fetches the first page; a truncated file reports
    // has_more, so surface the full size instead of silently cutting the text.
    const fileContent =
      workspaceContent.data &&
      workspaceRequest &&
      "path" in workspaceContent.data &&
      workspaceContent.data.path === workspaceRequest.relative_path &&
      "content" in workspaceContent.data
        ? workspaceContent.data.content
        : null;
    const officePath = doc.relativePath && workspacePath && isOfficePath(doc.relativePath)
      ? localFilePath(workspacePath, doc.relativePath)
      : null;
    return (
      <div className="flex h-full flex-col min-h-0">
        <div className="flex h-12 shrink-0 items-center justify-between border-b px-3">
          <h2 className="truncate text-body font-semibold">{doc.relativePath ?? doc.label ?? "File"}</h2>
        <div className="flex items-center gap-1">
          {officePath && canOpenLocalPath() && (
            <Button
              variant="ghost"
              size="sm"
              className="gap-1.5"
              onClick={() => { void openLocalPath(officePath); }}
            >
              <Pencil className="h-4 w-4" />
              {t(($) => $.doc_mode.open_in_office)}
            </Button>
          )}
          {workspaceContentText !== null && !doc.projectResourceId && (
            <Button variant={workspaceEditing ? "secondary" : "ghost"} size="sm" className="gap-1.5" onClick={openWorkspaceEdit}>
              <Pencil className="h-4 w-4" />
              {t(($) => $.doc_mode.edit)}
            </Button>
          )}
          {onAddToChat && workspaceContentText !== null && (
            <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => onAddToChat(documentQuote(doc.relativePath ?? doc.label ?? "File", workspaceContentText))}>
              <MessageSquareQuote className="h-4 w-4" />
              {t(($) => $.doc_mode.add_to_chat)}
            </Button>
          )}
          <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => closeDoc(sessionId)}>
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
        </div>
      {workspaceEditing ? (
        <DocEdit
          label={doc.relativePath ?? doc.label ?? "File"}
          content={workspaceEdited}
          dirty={workspaceEdited !== (workspaceContentText ?? "")}
          saving={workspaceSaving}
          error={workspaceSaveError}
          onChange={setWorkspaceEdited}
          onSave={() => void saveWorkspaceEdit()}
          onCancel={() => { setWorkspaceEditing(false); setWorkspaceSaveError(null); }}
          markdown={/\.mdx?$/i.test(doc.relativePath ?? doc.label ?? "")}
          saveLabel={t(($) => $.doc_mode.save)}
          savingLabel={t(($) => $.doc_mode.saving)}
          discardLabel={t(($) => $.doc_mode.discard)}
          changeLabel={t(($) => $.doc_mode.changed_reload)}
          failLabel={t(($) => $.doc_mode.save_failed)}
        />
      ) : (
        <WorkspaceFileBody
          loading={workspaceContent.isLoading || (!!doc.taskId && !sessionFiles && !workspacePath)}
          error={workspaceContent.isError || !workspacePath || !workspaceRuntimeId}
          onRetry={() => { void workspaceContent.refetch(); }}
          content={fileContent?.text ?? null}
          truncated={fileContent?.has_more ?? false}
          total={fileContent?.size ?? 0}
          label={doc.relativePath ?? doc.label ?? "File"}
          path={doc.relativePath ?? doc.label ?? "File"}
          onAddToChat={onAddToChat ? (selection) => onAddToChat(documentQuote(`${doc.relativePath ?? doc.label ?? "File"} 的选区`, selection)) : undefined}
        />
      )}
      </div>
    );
  }

  const isText = isTextAttachment || doc.contentType?.startsWith("text/") || current.data?.content != null;
  const bodyContent = isTextAttachment && attachmentTextContent.data
    ? attachmentTextContent.data.text
    : current.data?.content ?? null;
  const binaryAttachment = doc.kind === "attachment" && !isTextAttachment;
  // Editing is available for an issue's text document (the requirement doc) to
  // any workspace member — mirrors the backend, whose record handler gates on
  // requireUserID only. Attachments (binary) and workspace files are read-only.
  const canEdit = !!user && doc.kind === "issue" && isText && current.data?.content != null;

  return (
    <div className="flex h-full flex-col min-h-0">
      <div className="flex h-12 shrink-0 items-center justify-between border-b px-3">
        <h2 className="truncate text-body font-semibold">
          {doc.label ?? (doc.kind === "issue" ? t(($) => $.doc_mode.issue) : t(($) => $.doc_mode.attachment))}
        </h2>
        <div className="flex items-center gap-1">
          {canEdit && (
            <Button
              variant={editing ? "secondary" : "ghost"}
              size="sm"
              className="gap-1.5"
              onClick={openEdit}
            >
              <Pencil className="h-4 w-4" />
              {t(($) => $.doc_mode.edit)}
            </Button>
          )}
          <Button
            variant={diffView ? "secondary" : "ghost"}
            size="sm"
            className="gap-1.5"
            onClick={() => setDiffView(sessionId, !diffView)}
          >
            <GitCompare className="h-4 w-4" />
            {t(($) => $.doc_mode.diff)}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={() => closeDoc(sessionId)}
          >
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        {editing ? (
          <DocEdit
            label={doc.label ?? "Document"}
            content={edited}
            dirty={edited !== (current.data?.content ?? "")}
            saving={record.isPending}
            error={saveError}
            onChange={setEdited}
            onSave={save}
            onCancel={() => {
              setEditing(false);
              setSaveError(null);
            }}
            markdown={isText && (doc.contentType?.toLowerCase().split(";", 1)[0] === "text/markdown" || /\.mdx?$/i.test(doc.label ?? ""))}
            saveLabel={t(($) => $.doc_mode.save)}
            savingLabel={t(($) => $.doc_mode.saving)}
            discardLabel={t(($) => $.doc_mode.discard)}
            changeLabel={t(($) => $.doc_mode.changed_reload)}
            failLabel={t(($) => $.doc_mode.save_failed)}
          />
        ) : diffView ? (
          <DocVersionHistory
            versions={versions.data ?? []}
            loading={versions.isLoading}
            onRollback={(revision) => rollback.mutate({ ref: doc, revision })}
            rollbackLabel={t(($) => $.doc_mode.rollback)}
          />
        ) : (
          <DocBody
            loading={isTextAttachment ? attachmentTextContent.isLoading : binaryAttachment && attachmentRecord ? false : current.isLoading}
            error={isTextAttachment ? attachmentTextContent.isError : binaryAttachment && attachmentRecord ? false : current.isError}
            onRetry={() => { void (isTextAttachment ? attachmentTextContent.refetch() : current.refetch()); }}
            content={bodyContent}
            contentUrl={current.data?.content_url ?? null}
            attachment={attachmentRecord}
            contentType={doc.contentType ?? current.data?.content_type ?? "text/markdown"}
            isText={isText}
            label={doc.label ?? "Document"}
            onAddToChat={onAddToChat ? (selection) => onAddToChat(documentQuote(`${doc.label ?? "Document"} 的选区`, selection)) : undefined}
            parentIssueId={doc.kind === "issue" ? doc.id : undefined}
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
  onRetry,
  content,
  contentUrl,
  attachment,
  contentType,
  isText,
  label,
  onAddToChat,
  parentIssueId,
}: {
  loading: boolean;
  error: boolean;
  onRetry: () => void;
  content: string | null;
  contentUrl: string | null;
  attachment?: AttachmentRecord;
  contentType: string;
  isText: boolean;
  label: string;
  onAddToChat?: (content: string) => void;
  parentIssueId?: string;
}) {
  const { t } = useT("chat");
  // The selection toolbar anchors to this content container. It only raises for
  // the read-only text (RichContent) branch; the binary/attachment branches
  // have nothing selectable, so the container ref is never attached there.
  const containerRef = useRef<HTMLDivElement>(null);
  const { bubble } = useDocSelectionBubble({ containerRef, parentIssueId, onAddToChat });
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
        <p className="text-body">{label}</p>
        <Button variant="outline" size="sm" onClick={onRetry}>{t(($) => $.doc_mode.retry)}</Button>
      </div>
    );
  }
  // Binary (image / PDF): render via the attachment preview; never edited in
  // place (D3/D10). Text (Markdown/HTML): render inline via RichContent.
  if (!isText && attachment) {
    return (
      <div className="mx-auto flex min-h-full max-w-4xl items-start justify-center p-6">
        <Attachment attachment={{ kind: "record", attachment }} layout="block" />
      </div>
    );
  }
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
    <div ref={containerRef} className="mx-auto max-w-3xl p-6">
      <RichContent content={content ?? ""} className="doc-mode-body" />
      {bubble}
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
  onRetry,
  content,
  truncated,
  total,
  label,
  onAddToChat,
  path,
}: {
  loading: boolean;
  error: boolean;
  onRetry: () => void;
  content: string | null;
  truncated: boolean;
  total: number;
  label: string;
  onAddToChat?: (selection: string) => void;
  path: string;
}) {
  const { t } = useT("chat");
  // A selected passage raises a floating toolbar above it (mirroring
  // issue-detail) instead of a fixed bottom bar. The container ref is the
  // scrollable content area the selection is detected within.
  const containerRef = useRef<HTMLDivElement>(null);
  const { bubble } = useDocSelectionBubble({ containerRef, onAddToChat });
  const markdown = /\.mdx?$/i.test(path);
  const language = languageForPath(path);
  if (loading) {
    return <div className="flex h-full items-center justify-center text-muted-foreground"><Loader2 className="h-5 w-5 animate-spin" /></div>;
  }
  if (error || content === null) {
    return <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><p className="text-body">{label}</p><Button variant="outline" size="sm" onClick={onRetry}>{t(($) => $.doc_mode.retry)}</Button></div>;
  }
  return (
    <div className="flex h-full flex-col min-h-0">
      <div ref={containerRef} className="min-h-0 flex-1 overflow-auto p-6">
        {markdown ? (
          <div className="mx-auto max-w-3xl">
            <RichContent content={content} className="doc-mode-body" />
          </div>
        ) : language ? (
          <CodeBlockStatic
            body={content}
            language={language}
            lineNumbers
            wrap
            className="min-h-full rounded-lg p-4 text-caption"
          />
        ) : (
          <pre className="m-0 whitespace-pre-wrap font-mono text-caption">{content}</pre>
        )}
      </div>
      {bubble}
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
            <p className="text-body font-medium">{`v${v.revision}`}</p>
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

// Scope 1: a human edits the requirement document's Markdown body in place. The
// editor is the same FileViewer the skill pages use (a raw text/code editor,
// "codex-like"). Save commits a new content version; the server appends it and
// a confirmed requirement is auto-reopened to change (maybeReopenRequirementOnEdit).
// The draft is never clobbered by a concurrent current-version change: on a 409
// conflict the user sees the message and reloads, rather than losing their edits.
function DocEdit({
  label,
  content,
  dirty,
  saving,
  error,
  onChange,
  onSave,
  onCancel,
  saveLabel,
  savingLabel,
  discardLabel,
  changeLabel,
  failLabel,
  markdown = false,
}: {
  label: string;
  content: string;
  dirty: boolean;
  saving: boolean;
  error: string | null;
  onChange: (content: string) => void;
  onSave: () => void;
  onCancel: () => void;
  saveLabel: string;
  savingLabel: string;
  discardLabel: string;
  changeLabel: string;
  failLabel: string;
  markdown?: boolean;
}) {
  const { t } = useT("chat");
  const [mode, setMode] = useState<"source" | "preview">("source");
  return (
    <div className="flex h-full flex-col min-h-0">
      {markdown && (
        <div className="flex shrink-0 items-center gap-1 border-b px-3 py-1.5">
          <Button
            type="button"
            size="sm"
            variant={mode === "source" ? "secondary" : "ghost"}
            className="gap-1.5"
            onClick={() => setMode("source")}
          >
            <Code2 className="h-4 w-4" />
            {t(($) => $.doc_mode.source)}
          </Button>
          <Button
            type="button"
            size="sm"
            variant={mode === "preview" ? "secondary" : "ghost"}
            className="gap-1.5"
            onClick={() => setMode("preview")}
          >
            <Eye className="h-4 w-4" />
            {t(($) => $.doc_mode.preview)}
          </Button>
        </div>
      )}
      <div className="min-h-0 flex-1">
        {markdown && mode === "preview" ? (
          <ContentEditor
            value={content}
            onUpdate={(markdown) => onChange(markdown)}
            debounceMs={0}
            showBubbleMenu
          />
        ) : (
          <FileViewer
            path={label || "document"}
            content={content}
            mode="raw"
            readOnly={false}
            autoFocus
            onChange={onChange}
          />
        )}
      </div>
      {error ? (
        <div className="shrink-0 border-t px-4 py-2">
          <Alert variant="destructive">
            <AlertDescription>
              {error === "changed_reload" ? changeLabel : failLabel}
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      <div className="flex items-center justify-between gap-2 border-t px-4 py-2">
        <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
          {discardLabel}
        </Button>
        <Button
          size="sm"
          onClick={onSave}
          disabled={!dirty || saving || error === "changed_reload"}
        >
          {saving ? savingLabel : saveLabel}
        </Button>
      </div>
    </div>
  );
}
