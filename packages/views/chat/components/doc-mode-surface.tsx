"use client";

import { AlertTriangle, Check, X, GitCompare, Loader2, Pencil, MessageSquareQuote, Code2, Eye, FilePlus } from "@multica/ui/icons";
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
  documentDraftKey,
} from "@multica/core/chat";
import { chatSessionFilesOptions } from "@multica/core/chat/queries";
import {
  useDocumentContentVersionCurrent,
  useDocumentContentVersions,
  useDeleteDocumentContentVersion,
  useRecordDocumentContentVersion,
  useRollbackDocumentContentVersion,
} from "@multica/core/chat/document-content-version-queries";
import { DocVersionHistory } from "./doc-version-history";
import {
  useDocumentAutosave,
  type DocumentSaveStatus,
} from "./use-document-autosave";
import { RichContent } from "../../rich-content";
import { Attachment } from "../../editor/attachment";
import { useLocale, useT } from "../../i18n";
import { applyReviewFileEdit, requestReviewPage } from "../../platform/local-review-pages";
import { canOpenLocalPath, openLocalPath } from "../../platform";
import { createSafeId } from "@multica/core/utils";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { toast } from "sonner";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { Input } from "@multica/ui/components/ui/input";
import { CodeBlockStatic } from "../../editor/code-block-static";
import { CodeMirrorEditor, ContentEditor } from "../../editor";
import { languageForPath } from "../../common/task-transcript/diff-highlight";
import type { Attachment as AttachmentRecord } from "@multica/core/types";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  mergeDocumentVersions,
  renderMergedDocument,
} from "./document-merge";

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
  return [`> ${label}`, "", textCodeBlock(content)].join("\n");
}

function isOfficePath(path: string): boolean {
  return /\.(docx?|xlsx?|pptx?|odt|ods|odp|pages|numbers|key)$/i.test(path);
}

function localFilePath(root: string, relativePath: string): string | null {
  const normalized = relativePath.replaceAll("\\", "/").replace(/^\.\//, "");
  if (!normalized || normalized.startsWith("/") || normalized.split("/").some((part) => part === "..")) return null;
  return `${root.replace(/[\\/]+$/, "")}/${normalized}`;
}

function documentOutline(content: string): readonly {
  readonly level: number;
  readonly title: string;
}[] {
  return [...content.matchAll(/^(#{1,3})\s+(.+?)\s*#*\s*$/gm)]
    .map((match) => ({
      level: match[1]?.length ?? 1,
      title: match[2]?.trim() ?? "",
    }))
    .filter((entry) => entry.title.length > 0);
}

function documentCounts(content: string): {
  readonly words: number;
  readonly characters: number;
} {
  const normalized = content.trim();
  const words = normalized
    ? normalized.match(/[\p{L}\p{N}_]+|[\u3400-\u9fff]/gu)?.length ?? 0
    : 0;
  return { words, characters: [...content].length };
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

/** Displays the selected document while the session conversation stays mounted. */
export function DocModeSurface({ wsId, sessionId, onAddToChat }: { wsId: string; sessionId: string; onAddToChat?: (content: string) => void }) {
  const { t } = useT("chat");
  const doc = useDocModeStore(selectActiveDocument(sessionId));
  const working = useDocModeStore(selectWorking(sessionId));
  const diffView = useDocModeStore(selectDiffView(sessionId));
  const closeDoc = useDocModeStore((s) => s.closeDoc);
  const setDiffView = useDocModeStore((s) => s.setDiffView);
  const setDocumentDraft = useDocModeStore((s) => s.setDocumentDraft);
  const clearDocumentDraft = useDocModeStore((s) => s.clearDocumentDraft);
  const record = useRecordDocumentContentVersion(wsId);
  const deleteVersion = useDeleteDocumentContentVersion(wsId);
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const actorLabels = useMemo(() => {
    const labels: Record<string, string> = {};
    for (const member of members) labels[member.user_id] = member.name;
    for (const agent of agents) labels[agent.id] = agent.name;
    return labels;
  }, [agents, members]);
  const [editing, setEditing] = useState(false);
  const [edited, setEdited] = useState("");
  const [editBase, setEditBase] = useState<{ readonly content: string; readonly revision: number; readonly contentType: string } | null>(null);
  const [workspaceEditing, setWorkspaceEditing] = useState(false);
  const [workspaceEdited, setWorkspaceEdited] = useState("");
  const [workspaceEditBase, setWorkspaceEditBase] = useState("");
  const [workspaceSaveError, setWorkspaceSaveError] = useState<string | null>(null);
  const [workspaceSaving, setWorkspaceSaving] = useState(false);
  const [workspaceFullContent, setWorkspaceFullContent] = useState<string | null>(null);
  const [workspaceFullLoading, setWorkspaceFullLoading] = useState(false);
  const [workspaceFullError, setWorkspaceFullError] = useState(false);
  const [conflictOpen, setConflictOpen] = useState(false);
  const [mergeChoices, setMergeChoices] = useState<Readonly<Record<string, "local" | "remote">>>({});
  const [mergeSaving, setMergeSaving] = useState(false);
  const [mergeError, setMergeError] = useState(false);
  const [checkpointOpen, setCheckpointOpen] = useState(false);
  const [checkpointNote, setCheckpointNote] = useState("");
  const [cursor, setCursor] = useState({ line: 1, column: 1 });

  const documentIdentity = `${doc?.sessionId ?? sessionId}:${doc?.kind ?? "none"}:${doc?.id ?? ""}:${doc?.taskId ?? ""}:${doc?.projectResourceId ?? ""}:${doc?.relativePath ?? ""}`;
  const documentDraftId = doc ? documentDraftKey(doc) : null;
  useEffect(() => {
    // Restore a durable draft before the next document's queries settle.
    const draft = documentDraftId
      ? useDocModeStore.getState().draftsByDocument[documentDraftId]
      : undefined;
    setEditing(draft?.kind === "version");
    setEdited(draft?.kind === "version" ? draft.content : "");
    setEditBase(draft?.kind === "version" ? {
      content: draft.baseContent,
      revision: draft.baseRevision,
      contentType: draft.contentType,
    } : null);
    setWorkspaceEditing(draft?.kind === "workspace_file");
    setWorkspaceEdited(draft?.kind === "workspace_file" ? draft.content : "");
    setWorkspaceEditBase(draft?.kind === "workspace_file" ? draft.baseContent : "");
    setWorkspaceSaveError(null);
    setWorkspaceFullContent(null);
    setWorkspaceFullLoading(false);
    setWorkspaceFullError(false);
    setConflictOpen(false);
    setMergeChoices({});
    setMergeError(false);
    setCursor({ line: 1, column: 1 });
  }, [documentDraftId, documentIdentity]);

  const workspaceDirty = workspaceEditing && workspaceEdited !== workspaceEditBase;
  const versionDirty = editing && edited !== editBase?.content;

  useEffect(() => {
    if (!workspaceDirty && !versionDirty) return;
    const preventUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", preventUnload);
    return () => window.removeEventListener("beforeunload", preventUnload);
  }, [versionDirty, workspaceDirty]);

  // Hooks must run unconditionally; the empty id disables the queries below.
  const activeRef = doc ?? EMPTY_DOC;
  const isTextAttachment = doc?.kind === "attachment" && (
    doc.contentType?.startsWith("text/") ||
    /\.(md|mdx|txt|json|ya?ml|csv|log|py|ts|tsx|js|jsx|go|rs|java|css|html)$/i.test(doc.label ?? "")
  );
  const current = useDocumentContentVersionCurrent(wsId, activeRef, { loadAttachments: !isTextAttachment || diffView });
  const versions = useDocumentContentVersions(wsId, activeRef, { loadAttachments: diffView });
  const attachmentTextContent = useQuery({
    queryKey: ["attachment-text-content", wsId, doc?.id],
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
  const workspaceContentPage =
    workspaceRequest &&
    !workspaceContent.isFetching &&
    workspaceContent.data &&
    "path" in workspaceContent.data &&
    workspaceContent.data.path === workspaceRequest.relative_path
      ? workspaceContent.data
      : null;
  const workspaceContentText =
    workspaceContentPage &&
    "content" in workspaceContentPage &&
    !workspaceContentPage.content.has_more
      ? fileContentText(workspaceContentPage)
      : null;
  const workspaceReadableContent = workspaceFullContent ?? workspaceContentText;

  const loadFullWorkspaceContent = useCallback(async () => {
    if (!workspaceRequest) return;
    setWorkspaceFullLoading(true);
    setWorkspaceFullError(false);
    try {
      const chunks: string[] = [];
      let offset = 0;
      for (;;) {
        const page = await requestReviewPage({
          ...workspaceRequest,
          offset,
          limit: 64 * 1024,
        });
        if (
          !("content" in page) ||
          page.path !== workspaceRequest.relative_path
        ) {
          throw new Error("workspace full-content response mismatch");
        }
        chunks.push(page.content.text);
        if (!page.content.has_more) break;
        if (page.content.next_offset <= offset || chunks.length >= 1024) {
          throw new Error("workspace full-content pagination did not advance");
        }
        offset = page.content.next_offset;
      }
      setWorkspaceFullContent(chunks.join(""));
    } catch (error) {
      if (error instanceof Error) setWorkspaceFullError(true);
      else throw error;
    } finally {
      setWorkspaceFullLoading(false);
    }
  }, [workspaceRequest]);

  const openWorkspaceEdit = useCallback(() => {
    if (workspaceReadableContent === null) return;
    setWorkspaceEdited(workspaceReadableContent);
    setWorkspaceEditBase(workspaceReadableContent);
    setWorkspaceSaveError(null);
    setWorkspaceEditing(true);
  }, [workspaceReadableContent]);

  const updateWorkspaceDraft = useCallback((content: string) => {
    setWorkspaceEdited(content);
    if (!doc || doc.kind !== "workspace_file") return;
    setDocumentDraft(doc, {
      kind: "workspace_file",
      content,
      baseContent: workspaceEditBase,
    });
  }, [doc, setDocumentDraft, workspaceEditBase]);

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
        edit_base: workspaceEditBase,
        edit_new: workspaceEdited,
      });
      if (result?.conflict) {
        setWorkspaceSaveError("changed_reload");
        await workspaceContent.refetch();
        return;
      }
      clearDocumentDraft(doc);
      setWorkspaceEditing(false);
      setWorkspaceEdited("");
      setWorkspaceEditBase("");
      await workspaceContent.refetch();
    } catch {
      setWorkspaceSaveError("save_failed");
    } finally {
      setWorkspaceSaving(false);
    }
  }, [clearDocumentDraft, doc, workspacePath, workspaceRequest, workspaceRuntimeId, workspaceContent, workspaceEditBase, workspaceEdited]);

  const openEdit = useCallback(() => {
    if (current.data?.content == null) return;
    const editSnapshot = {
      content: current.data.content,
      revision: current.data.owner_revision ?? current.data.revision,
      contentType: current.data.content_type ?? "text/markdown",
    };
    setEditBase(editSnapshot);
    setEdited(editSnapshot.content);
    setEditing(true);
    setDiffView(sessionId, false);
  }, [current.data, setDiffView, sessionId]);

  const updateVersionDraft = useCallback((content: string) => {
    setEdited(content);
    if (!doc || !editBase) return;
    setDocumentDraft(doc, {
      kind: "version",
      content,
      baseContent: editBase.content,
      baseRevision: editBase.revision,
      contentType: editBase.contentType,
    });
  }, [doc, editBase, setDocumentDraft]);

  const persistVersion = useCallback(
    async ({
      content,
      revision,
      contentType,
      merge,
      note,
    }: {
      readonly content: string;
      readonly revision: number;
      readonly contentType: string;
      readonly merge: boolean;
      readonly note?: string;
    }) => {
      if (!doc) throw new Error("document unavailable");
      return record.mutateAsync({
        ref: doc,
        input: {
          content_type: contentType,
          content,
          expected_revision: revision,
          merge,
          session_id: sessionId,
          ...(note ? { note } : {}),
        },
      });
    },
    [doc, record, sessionId],
  );
  const openConflict = useCallback(() => {
    setConflictOpen(true);
    void current.refetch();
  }, [current]);
  const autosave = useDocumentAutosave({
    enabled: editing && doc?.kind === "issue" && !!editBase,
    content: edited,
    base: editBase,
    save: persistVersion,
    onBaseChange: setEditBase,
    onSaved: (next) => {
      if (!doc) return;
      clearDocumentDraft(doc);
      setEditBase(next);
    },
    onConflict: openConflict,
  });
  useEffect(() => {
    if (!editing) {
      autosave.resetStatus("saved");
      return;
    }
    const draft = doc ? useDocModeStore.getState().draftsByDocument[documentDraftKey(doc)] : undefined;
    autosave.resetStatus(draft?.kind === "version" ? "dirty" : "saved");
    // The document identity owns this status, not the previous document.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [documentIdentity, editing]);
  const save = useCallback(() => {
    void autosave.saveNow(true);
  }, [autosave]);
  const createCheckpoint = useCallback(() => {
    setCheckpointNote("");
    setCheckpointOpen(true);
  }, []);
  const finishEditing = useCallback(async () => {
    if (!editing) return;
    const saved = await autosave.saveNow(true);
    if (saved || !versionDirty) {
      setEditing(false);
      setDiffView(sessionId, false);
    }
  }, [autosave, editing, sessionId, setDiffView, versionDirty]);
  const leaveEditor = useCallback(() => {
    if (versionDirty) void autosave.saveNow(true);
    setEditing(false);
    setDiffView(sessionId, false);
  }, [autosave, sessionId, setDiffView, versionDirty]);

  useEffect(() => {
    if (!editing) return;
    const onShortcut = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey) return;
      if (event.shiftKey && event.key.toLowerCase() === "h") {
        event.preventDefault();
        setDiffView(sessionId, !diffView);
      } else if (event.shiftKey && event.key.toLowerCase() === "s") {
        event.preventDefault();
        createCheckpoint();
      } else if (event.key === "Enter") {
        event.preventDefault();
        void finishEditing();
      }
    };
    window.addEventListener("keydown", onShortcut);
    return () => window.removeEventListener("keydown", onShortcut);
  }, [
    createCheckpoint,
    diffView,
    editing,
    finishEditing,
    sessionId,
    setDiffView,
  ]);
  const closeDocument = () => {
    setEditing(false);
    setEdited("");
    setEditBase(null);
    setWorkspaceEditing(false);
    setWorkspaceEdited("");
    setWorkspaceEditBase("");
    closeDoc(sessionId);
  };
  const latestContent = current.data?.content ?? null;
  const latestBase = latestContent !== null && current.data
    ? {
        content: latestContent,
        revision: current.data.owner_revision ?? current.data.revision,
        contentType: current.data.content_type ?? editBase?.contentType ?? "text/markdown",
      }
    : null;
  const conflictMerge = editBase && latestBase && conflictOpen
    ? mergeDocumentVersions({
        base: editBase.content,
        local: edited,
        remote: latestBase.content,
      })
    : null;
  const mergedConflictContent = conflictMerge
    ? renderMergedDocument(conflictMerge, mergeChoices)
    : null;
  const unresolvedConflicts = conflictMerge
    ? conflictMerge.segments.filter(
        (segment) => segment.kind === "conflict" && !mergeChoices[segment.id],
      ).length
    : 0;
  useEffect(() => {
    setMergeChoices({});
    setMergeError(false);
    setMergeSaving(false);
  }, [latestBase?.revision]);
  const keepLocalDraft = () => {
    if (!latestBase) return;
    setEditBase(latestBase);
    autosave.rebase(latestBase);
    setConflictOpen(false);
    void autosave.saveNow(true);
  };
  const loadLatestVersion = () => {
    if (!latestBase || !doc) return;
    clearDocumentDraft(doc);
    setEdited(latestBase.content);
    setEditBase(latestBase);
    setConflictOpen(false);
    autosave.resetStatus("saved");
  };
  const saveMergedDraft = async () => {
    if (!latestBase || !mergedConflictContent || !doc) return;
    setMergeSaving(true);
    setMergeError(false);
    try {
      const saved = await persistVersion({
        content: mergedConflictContent,
        revision: latestBase.revision,
        contentType: latestBase.contentType,
        merge: false,
        note: t(($) => $.doc_mode.merge_resolution_note),
      });
      const nextBase = {
        content: mergedConflictContent,
        revision: saved.owner_revision ?? saved.revision,
        contentType: latestBase.contentType,
      };
      setEdited(mergedConflictContent);
      setEditBase(nextBase);
      clearDocumentDraft(doc);
      setConflictOpen(false);
      autosave.resetStatus("saved");
    } catch (error) {
      if (error instanceof Error) setMergeError(true);
      else throw error;
    } finally {
      setMergeSaving(false);
    }
  };
  const conflictDialog = (
    <AlertDialog open={conflictOpen} onOpenChange={setConflictOpen}>
      <AlertDialogContent className="w-[calc(100vw-2rem)] !max-w-6xl">
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.doc_mode.conflict_title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.doc_mode.merge_conflict_description)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="grid max-h-[58dvh] gap-3 overflow-y-auto md:grid-cols-3">
          <section className="min-w-0 rounded-md border bg-muted/30 p-3">
            <h3 className="mb-2 text-caption font-medium">{t(($) => $.doc_mode.your_draft)}</h3>
            <pre className="whitespace-pre-wrap break-words font-mono text-caption">{edited}</pre>
          </section>
          <section className="min-w-0 rounded-md border bg-muted/30 p-3">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-caption font-medium">{t(($) => $.doc_mode.merge_result)}</h3>
              <span className={`text-caption ${unresolvedConflicts ? "text-warning" : "text-success"}`}>
                {unresolvedConflicts
                  ? t(($) => $.doc_mode.merge_conflicts_remaining, {
                      count: unresolvedConflicts,
                    })
                  : t(($) => $.doc_mode.merge_ready)}
              </span>
            </div>
            {conflictMerge ? (
              <div className="space-y-1 font-mono text-caption">
                {conflictMerge.segments.map((segment) =>
                  segment.kind === "lines" ? (
                    <pre key={segment.id} className="whitespace-pre-wrap break-words">{segment.lines.join("\n")}</pre>
                  ) : (
                    <section
                      key={segment.id}
                      aria-label={t(($) => $.doc_mode.merge_conflict_block)}
                      className="rounded-md border border-warning/40 bg-warning/5 p-2"
                    >
                      <div className="flex items-start gap-1.5 text-warning">
                        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                        <span>{t(($) => $.doc_mode.merge_conflict_block)}</span>
                      </div>
                      {mergeChoices[segment.id] ? (
                        <pre className="mt-2 whitespace-pre-wrap break-words">
                          {(mergeChoices[segment.id] === "local" ? segment.local : segment.remote).join("\n")}
                        </pre>
                      ) : (
                        <p className="mt-2 text-muted-foreground">{t(($) => $.doc_mode.merge_choose_side)}</p>
                      )}
                      <div className="mt-2 flex flex-wrap gap-1">
                        <Button
                          size="xs"
                          variant={mergeChoices[segment.id] === "local" ? "secondary" : "outline"}
                          aria-pressed={mergeChoices[segment.id] === "local"}
                          onClick={() => setMergeChoices((current) => ({ ...current, [segment.id]: "local" }))}
                        >
                          {mergeChoices[segment.id] === "local" && <Check aria-hidden="true" />}
                          {t(($) => $.doc_mode.use_local_block)}
                        </Button>
                        <Button
                          size="xs"
                          variant={mergeChoices[segment.id] === "remote" ? "secondary" : "outline"}
                          aria-pressed={mergeChoices[segment.id] === "remote"}
                          onClick={() => setMergeChoices((current) => ({ ...current, [segment.id]: "remote" }))}
                        >
                          {mergeChoices[segment.id] === "remote" && <Check aria-hidden="true" />}
                          {t(($) => $.doc_mode.use_server_block)}
                        </Button>
                      </div>
                    </section>
                  ),
                )}
              </div>
            ) : (
              <p className="text-muted-foreground">{t(($) => $.doc_mode.loading_latest)}</p>
            )}
          </section>
          <section className="min-w-0 rounded-md border bg-muted/30 p-3">
            <h3 className="mb-2 text-caption font-medium">{t(($) => $.doc_mode.latest_version)}</h3>
            <pre className="whitespace-pre-wrap break-words font-mono text-caption">{latestContent ?? t(($) => $.doc_mode.loading_latest)}</pre>
          </section>
        </div>
        {mergeError && (
          <p role="alert" className="text-caption text-destructive">
            {t(($) => $.doc_mode.merge_save_failed)}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>{t(($) => $.doc_mode.keep_editing)}</AlertDialogCancel>
          <Button variant="outline" disabled={!latestBase} onClick={loadLatestVersion}>
            {t(($) => $.doc_mode.load_latest)}
          </Button>
          <Button
            variant="secondary"
            disabled={!mergedConflictContent || unresolvedConflicts > 0 || mergeSaving}
            onClick={() => void saveMergedDraft()}
          >
            {mergeSaving ? <Loader2 className="animate-spin" aria-hidden="true" /> : <GitCompare aria-hidden="true" />}
            {t(($) => $.doc_mode.save_merged)}
          </Button>
          <AlertDialogAction disabled={!latestBase} onClick={keepLocalDraft}>
            {t(($) => $.doc_mode.keep_my_draft)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
  const checkpointDialog = (
    <AlertDialog open={checkpointOpen} onOpenChange={setCheckpointOpen}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.doc_mode.checkpoint_title)}</AlertDialogTitle>
          <AlertDialogDescription>{t(($) => $.doc_mode.checkpoint_description)}</AlertDialogDescription>
        </AlertDialogHeader>
        <Input
          autoFocus
          aria-label={t(($) => $.doc_mode.checkpoint_note)}
          value={checkpointNote}
          maxLength={200}
          onChange={(event) => setCheckpointNote(event.target.value)}
          placeholder={t(($) => $.doc_mode.checkpoint_note)}
        />
        <AlertDialogFooter>
          <AlertDialogCancel>{t(($) => $.doc_mode.cancel)}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              setCheckpointOpen(false);
              void autosave.saveNow(false, checkpointNote.trim() || undefined);
            }}
          >
            {t(($) => $.doc_mode.save_checkpoint)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
  const saveError = autosave.status === "conflict"
    ? "changed_reload"
    : autosave.status === "error"
      ? "save_failed"
      : null;

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
        <div className="flex min-h-12 shrink-0 flex-wrap items-center justify-between gap-2 border-b px-3 py-2">
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="max-w-full truncate text-body font-semibold">{doc.relativePath ?? doc.label ?? "File"}</h2>
            <DocumentSaveIndicator
              status={workspaceSaving ? "saving" : workspaceDirty ? "dirty" : "saved"}
            />
          </div>
        <div className="flex flex-wrap items-center gap-1">
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
          {workspaceReadableContent !== null && !doc.projectResourceId && (
            <Button variant={workspaceEditing ? "secondary" : "ghost"} size="sm" className="gap-1.5" onClick={openWorkspaceEdit} disabled={workspaceEditing}>
              <Pencil className="h-4 w-4" />
              {t(($) => $.doc_mode.edit)}
            </Button>
          )}
          {onAddToChat && workspaceReadableContent !== null && (
            <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => onAddToChat(documentQuote(doc.relativePath ?? doc.label ?? "File", workspaceReadableContent))}>
              <MessageSquareQuote className="h-4 w-4" />
              {t(($) => $.doc_mode.add_to_chat)}
            </Button>
          )}
          <Button variant="ghost" size="sm" className="gap-1.5" onClick={closeDocument}>
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
        </div>
      {workspaceEditing ? (
        <DocEdit
          label={doc.relativePath ?? doc.label ?? "File"}
          content={workspaceEdited}
          saving={workspaceSaving}
          error={workspaceSaveError}
          onChange={updateWorkspaceDraft}
          onSave={() => void saveWorkspaceEdit()}
          onCancel={() => {
            setWorkspaceEditing(false);
            setWorkspaceEdited("");
            setWorkspaceEditBase("");
            setWorkspaceSaveError(null);
          }}
          markdown={/\.mdx?$/i.test(doc.relativePath ?? doc.label ?? "")}
          saveLabel={t(($) => $.doc_mode.apply_to_workspace)}
          savingLabel={t(($) => $.doc_mode.saving)}
          doneLabel={t(($) => $.doc_mode.done)}
          changeLabel={t(($) => $.doc_mode.changed_reload)}
          failLabel={t(($) => $.doc_mode.save_failed)}
          saveShortcutLabel={t(($) => $.doc_mode.save_shortcut)}
          onCursorChange={setCursor}
          preserveDraftOnCancel
        />
      ) : (
        <WorkspaceFileBody
          loading={workspaceContent.isLoading || (!!doc.taskId && !sessionFiles && !workspacePath)}
          error={workspaceContent.isError || !workspacePath || !workspaceRuntimeId}
          onRetry={() => { void workspaceContent.refetch(); }}
          content={workspaceFullContent ?? fileContent?.text ?? null}
          truncated={workspaceFullContent === null && (fileContent?.has_more ?? false)}
          total={fileContent?.size ?? 0}
          fullLoading={workspaceFullLoading}
          fullError={workspaceFullError}
          onLoadFull={() => void loadFullWorkspaceContent()}
          label={doc.relativePath ?? doc.label ?? "File"}
          path={doc.relativePath ?? doc.label ?? "File"}
          onAddToChat={onAddToChat ? (selection) => onAddToChat(documentQuote(doc.relativePath ?? doc.label ?? "File", selection)) : undefined}
        />
      )}
      {conflictDialog}
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
  // requireUserID only. Workspace-file edits use the separate merge flow above.
  const canEdit = !!user && doc.kind === "issue" && isText && current.data?.content != null;

  return (
    <div className="flex h-full flex-col min-h-0">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="max-w-full truncate text-body font-semibold">
            {doc.label ?? (doc.kind === "issue" ? t(($) => $.doc_mode.issue) : t(($) => $.doc_mode.attachment))}
          </h2>
          <DocumentSaveIndicator
            status={autosave.status}
            lastSavedAt={autosave.lastSavedAt}
          />
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {canEdit && (
            <Button
              variant={editing ? "secondary" : "ghost"}
              size="sm"
              className="gap-1.5"
              onClick={openEdit}
              disabled={editing}
            >
              <Pencil className="h-4 w-4" />
              {t(($) => $.doc_mode.edit)}
            </Button>
          )}
          {onAddToChat && bodyContent !== null && (
            <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => onAddToChat(documentQuote(doc.label ?? "Document", bodyContent))}>
              <MessageSquareQuote className="h-4 w-4" />
              {t(($) => $.doc_mode.add_to_chat)}
            </Button>
          )}
          <Button
            variant={diffView ? "secondary" : "ghost"}
            size="sm"
            className="gap-1.5"
            onClick={() => setDiffView(sessionId, !diffView)}
          >
            <GitCompare className="h-4 w-4" />
            {t(($) => $.doc_mode.history)}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={closeDocument}
          >
            <X className="h-4 w-4" />
            {t(($) => $.doc_mode.close)}
          </Button>
        </div>
      </div>

      <div className="relative flex min-h-0 flex-1 overflow-hidden">
        <div className="flex min-h-0 min-w-0 flex-1">
          {doc.kind === "issue" && (
            <aside className="hidden w-52 shrink-0 overflow-y-auto border-r bg-muted/10 p-3 xl:block" aria-label={t(($) => $.doc_mode.outline)}>
              <DocOutline content={editing ? edited : bodyContent ?? ""} />
            </aside>
          )}
          <div className="min-w-0 flex-1 overflow-y-auto">
            {editing ? (
              <DocEdit
                label={doc.label ?? "Document"}
                content={edited}
                saving={autosave.status === "saving"}
                error={saveError}
                onChange={updateVersionDraft}
                onAutosave={save}
                onSave={createCheckpoint}
                onSaveAndRead={() => void finishEditing()}
                onCancel={leaveEditor}
                onCursorChange={setCursor}
                markdown={isText && (doc.contentType?.toLowerCase().split(";", 1)[0] === "text/markdown" || /\.mdx?$/i.test(doc.label ?? ""))}
                saveLabel={t(($) => $.doc_mode.save_checkpoint)}
                savingLabel={t(($) => $.doc_mode.saving)}
                doneLabel={t(($) => $.doc_mode.done)}
                changeLabel={t(($) => $.doc_mode.changed_reload)}
                failLabel={t(($) => $.doc_mode.save_failed)}
                saveShortcutLabel={t(($) => $.doc_mode.save_shortcut)}
                preserveDraftOnCancel
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
              onAddToChat={onAddToChat ? (selection) => onAddToChat(documentQuote(doc.label ?? "Document", selection)) : undefined}
              parentIssueId={doc.kind === "issue" ? doc.id : undefined}
            />
            )}
          </div>
        </div>
        {diffView && (
          <aside className="absolute inset-0 z-10 flex w-full shrink-0 flex-col overflow-hidden border-l bg-card md:static md:w-80">
            <div className="flex items-center justify-between border-b px-3 py-2">
              <h3 className="text-caption font-semibold">{t(($) => $.doc_mode.history)}</h3>
              <Button size="icon-xs" variant="ghost" aria-label={t(($) => $.doc_mode.close_history)} onClick={() => setDiffView(sessionId, false)}>
                <X aria-hidden="true" />
              </Button>
            </div>
            <DocVersionHistory
              versions={versions.data ?? []}
              current={current.data}
              actorLabels={actorLabels}
              diffLabel={t(($) => $.doc_mode.diff)}
              loading={versions.isLoading}
              onRollback={(revision) => rollback.mutate({ ref: doc, revision })}
              onDelete={(revision) => deleteVersion.mutateAsync({ ref: doc, revision })}
              onAddToChat={onAddToChat ? (content) => onAddToChat(documentQuote(doc.label ?? "Document", content)) : undefined}
              rollbackLabel={t(($) => $.doc_mode.rollback)}
              rollbackConfirmTitle={t(($) => $.doc_mode.rollback_confirm_title)}
              rollbackConfirmDescription={t(($) => $.doc_mode.rollback_confirm_description)}
              cancelLabel={t(($) => $.doc_mode.cancel)}
            />
          </aside>
        )}
      </div>

      <DocumentStatusBar
        content={editing ? edited : bodyContent ?? ""}
        cursor={cursor}
        draft={versionDirty || workspaceDirty}
        editing={editing || workspaceEditing}
      />
      <FloatingWorkingIndicator
        working={working}
        label={doc.label ?? "Document"}
        workingLabel={t(($) => $.doc_mode.working)}
      />
      {conflictDialog}
      {checkpointDialog}
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
  if (!isText && attachment && !contentUrl) {
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

function DocumentSaveIndicator({
  status,
  lastSavedAt,
}: {
  readonly status: DocumentSaveStatus;
  readonly lastSavedAt?: number | null;
}) {
  const { t } = useT("chat");
  const locale = useLocale();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (status !== "saved" || lastSavedAt === null || lastSavedAt === undefined) {
      return;
    }
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [lastSavedAt, status]);
  const elapsed = lastSavedAt ? Math.max(0, Math.floor((now - lastSavedAt) / 1_000)) : 0;
  const label =
    status === "saving"
      ? t(($) => $.doc_mode.saving)
      : status === "offline"
        ? t(($) => $.doc_mode.offline_draft)
        : status === "conflict"
          ? t(($) => $.doc_mode.conflict_short)
          : status === "error"
            ? t(($) => $.doc_mode.save_failed)
            : status === "dirty"
              ? t(($) => $.doc_mode.local_draft)
              : lastSavedAt === null || lastSavedAt === undefined
                ? t(($) => $.doc_mode.saved)
                : elapsed < 5
                  ? t(($) => $.doc_mode.auto_saved)
                  : elapsed < 60
                    ? t(($) => $.doc_mode.saved_seconds_ago, { count: elapsed })
                    : t(($) => $.doc_mode.saved_minutes_ago, { count: Math.floor(elapsed / 60) })
  return (
    <span
      role="status"
      aria-live="polite"
      title={
        status === "saved" && lastSavedAt
          ? new Date(lastSavedAt).toLocaleTimeString(locale)
          : undefined
      }
      className={
        status === "conflict" || status === "error"
          ? "shrink-0 text-caption text-destructive"
          : status === "offline"
            ? "shrink-0 text-caption text-warning"
            : "shrink-0 text-caption text-muted-foreground"
      }
    >
      {label}
    </span>
  );
}

function DocOutline({ content }: { readonly content: string }) {
  const { t } = useT("chat");
  const headings = documentOutline(content);
  return (
    <div>
      <h3 className="mb-3 text-caption font-semibold text-muted-foreground">
        {t(($) => $.doc_mode.outline)}
      </h3>
      {headings.length ? (
        <ol className="space-y-1">
          {headings.map((heading, index) => (
            <li
              key={`${heading.level}:${heading.title}:${index}`}
              className="break-words text-caption text-muted-foreground"
              style={{ paddingInlineStart: `${(heading.level - 1) * 0.75}rem` }}
            >
              {heading.title}
            </li>
          ))}
        </ol>
      ) : (
        <p className="text-caption text-faint-foreground">
          {t(($) => $.doc_mode.no_outline)}
        </p>
      )}
    </div>
  );
}

function DocumentStatusBar({
  content,
  cursor,
  draft,
  editing,
}: {
  readonly content: string;
  readonly cursor: { readonly line: number; readonly column: number };
  readonly draft: boolean;
  readonly editing: boolean;
}) {
  const { t } = useT("chat");
  const counts = useMemo(() => documentCounts(content), [content]);
  return (
    <footer className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-t px-3 py-1 text-caption text-muted-foreground">
      {editing && (
        <span className="tabular-nums">
          {t(($) => $.doc_mode.cursor_position, cursor)}
        </span>
      )}
      <span className="tabular-nums">
        {t(($) => $.doc_mode.word_count, { count: counts.words })}
      </span>
      <span className="tabular-nums">
        {t(($) => $.doc_mode.character_count, { count: counts.characters })}
      </span>
      {draft && (
        <span className="text-warning">{t(($) => $.doc_mode.local_draft)}</span>
      )}
      {editing && (
        <span className="ml-auto hidden md:inline">
          {t(($) => $.doc_mode.editor_shortcuts)}
        </span>
      )}
    </footer>
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
  fullLoading,
  fullError,
  onLoadFull,
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
  fullLoading?: boolean;
  fullError?: boolean;
  onLoadFull?: () => void;
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
        <div className="flex flex-wrap items-center justify-between gap-2 border-t px-6 py-2 text-caption text-muted-foreground">
          <span>{t(($) => $.doc_mode.truncated, { total: formatBytes(total) })}</span>
          {onLoadFull && (
            <Button size="xs" variant="outline" disabled={fullLoading} onClick={onLoadFull}>
              {fullLoading && <Loader2 className="animate-spin" aria-hidden="true" />}
              {t(($) => $.doc_mode.load_full_content)}
            </Button>
          )}
          {fullError && <span role="alert" className="text-destructive">{t(($) => $.doc_mode.load_full_failed)}</span>}
        </div>
      )}
    </div>
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
// editor is a CodeMirror source surface shared with code-oriented views. Save
// creates a named content checkpoint; the server appends it and
// a confirmed requirement is auto-reopened to change (maybeReopenRequirementOnEdit).
// The draft is never clobbered by a concurrent current-version change: on a 409
// conflict the user sees the message and reloads, rather than losing their edits.
function DocEdit({
  label,
  content,
  saving,
  error,
  onChange,
  onAutosave,
  onSave,
  onSaveAndRead,
  onCancel,
  onCursorChange,
  saveLabel,
  savingLabel,
  doneLabel,
  changeLabel,
  failLabel,
  saveShortcutLabel,
  markdown = false,
  preserveDraftOnCancel = false,
}: {
  label: string;
  content: string;
  saving: boolean;
  error: string | null;
  onChange: (content: string) => void;
  onAutosave?: () => void;
  onSave: () => void;
  onSaveAndRead?: () => void;
  onCancel: () => void;
  onCursorChange?: (position: {
    readonly line: number;
    readonly column: number;
  }) => void;
  saveLabel: string;
  savingLabel: string;
  doneLabel: string;
  changeLabel: string;
  failLabel: string;
  saveShortcutLabel: string;
  markdown?: boolean;
  preserveDraftOnCancel?: boolean;
}) {
  const { t } = useT("chat");
  const [mode, setMode] = useState<"source" | "preview">("source");
  useEffect(() => {
    if (!preserveDraftOnCancel) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && mode === "preview") {
        event.preventDefault();
        onCancel();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mode, onCancel, preserveDraftOnCancel]);
  return (
    <div
      className="flex h-full flex-col min-h-0"
      onKeyDown={(event) => {
        if (!markdown || mode !== "preview") return;
        if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === "s") {
          event.preventDefault();
          onSave();
        } else if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
          event.preventDefault();
          onAutosave?.();
        } else if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
          event.preventDefault();
          onSaveAndRead?.();
        }
      }}
    >
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
      <div className="mx-auto min-h-0 w-full max-w-3xl flex-1">
        {markdown && mode === "preview" ? (
          <ContentEditor
            value={content}
            onUpdate={(markdown) => onChange(markdown)}
            debounceMs={0}
            showBubbleMenu
          />
        ) : (
          <CodeMirrorEditor
            path={label || "document"}
            label={label || t(($) => $.doc_mode.source)}
            value={content}
            onChange={onChange}
            onSave={onAutosave ?? onSave}
            onCheckpoint={onSave}
            onExit={onCancel}
            onCursorChange={onCursorChange}
            autoFocus
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
        <Button
          variant="ghost"
          size="sm"
          onClick={onCancel}
          disabled={saving}
        >
          {doneLabel}
        </Button>
        <Button
          size="sm"
          onClick={onSave}
          disabled={saving || error === "changed_reload"}
          title={saveShortcutLabel}
        >
          {saving ? savingLabel : saveLabel}
        </Button>
      </div>
    </div>
  );
}
