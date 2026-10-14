"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Alert } from "@multica/ui/components/ui/alert";
import {
  useApplyRuntimeFileEdit,
  useRuntimeFileEditDraftStore,
  hasRuntimeFileEdit,
} from "@multica/core/runtimes";
import { ApiError } from "@multica/core/api";
import { FileViewer } from "../../skills/components/file-viewer";
import { useT } from "../../i18n";

// A runtime working file's edit surface. It reuses FileViewer's read/write
// (preview/raw) and routes the change through api.applyRuntimeFileEdit. Two
// write modes, chosen by the transport:
//   - remote (web): a client draft submitted as a three-way merge; an
//     unresolvable conflict keeps the runtime version (conflict: true) and the
//     panel rebases onto the runtime content.
//   - local: a compare-and-swap write; a changed base yields 409 and the panel
//     asks the parent to reload the current content.
// Binary files are read-only and never enter edit mode.
export function RuntimeFileEditPanel({
  taskId,
  path,
  target,
  relativePath,
  baseContent,
  baseSha256,
  isBinary,
  canEdit,
  isRemote = true,
  onReload,
}: {
  taskId: string;
  path: string;
  target: string;
  relativePath: string;
  baseContent: string;
  // Digest of the opened content; required for the local compare-and-swap write.
  baseSha256: string;
  isBinary: boolean;
  canEdit: boolean;
  // Remote runtimes (web) merge against the runtime; local runtimes compare
  // and swap. Defaults to remote (the web path).
  isRemote?: boolean;
  // Reload the current content after a changed base (local 409).
  onReload?: () => void;
}) {
  const { t } = useT("runtimes");
  const [mode, setMode] = useState<"preview" | "raw">("preview");
  const [editing, setEditing] = useState(false);
  const apply = useApplyRuntimeFileEdit();

  const fileReadOnly = isBinary || !canEdit || !editing;
  // The edit affordance is available for any editable, non-binary file,
  // independent of whether we are currently in edit mode.
  const canToggleEdit = canEdit && !isBinary;
  const key = useRuntimeFileEditDraftStore((s) => s.key(taskId, relativePath));
  const draft = useRuntimeFileEditDraftStore((s) => s.drafts[key]);
  const setEdited = useRuntimeFileEditDraftStore((s) => s.setEdited);
  const openDraft = useRuntimeFileEditDraftStore((s) => s.open);
  const discard = useRuntimeFileEditDraftStore((s) => s.discard);

  // Seed the draft when the file is opened; the parent re-seeds on a new base.
  useEffect(() => {
    openDraft(taskId, relativePath, {
      base: baseContent,
      baseSha256,
      edited: baseContent,
    });
  }, [openDraft, taskId, relativePath, baseContent, baseSha256]);

  const edited = draft?.edited ?? baseContent;
  const dirty = hasRuntimeFileEdit(draft?.base ?? baseContent, edited);

  // A conflict keeps the runtime version (result.new_content); adopt it as the
  // new base and stop showing the conflict so the editor reflects reality.
  const [conflictMessage, setConflictMessage] = useState<string | null>(null);

  const onEditChange = useCallback(
    (next: string) => setEdited(taskId, relativePath, next),
    [setEdited, taskId, relativePath],
  );

  const onEditToggle = useCallback(() => {
    if (!canToggleEdit) return;
    setEditing((next) => !next);
    if (!editing) setMode("raw");
  }, [canToggleEdit, editing]);

  const save = useCallback(async () => {
    if (!dirty) return;
    const request = {
      task_id: taskId,
      path,
      target,
      relative_path: relativePath,
      command_id: `${relativePath}:${Date.now()}`,
      action: (isRemote ? "merge_edit" : "edit") as "edit" | "merge_edit",
      edit_base: isRemote ? (draft?.base ?? baseContent) : undefined,
      edit_base_sha: isRemote ? undefined : baseSha256,
      edit_new: edited,
    };
    try {
      const result = await apply.mutateAsync(request);
      if (result?.conflict) {
        // The runtime version won; rebase onto its content and tell the user.
        setConflictMessage(t(($) => $.file_edit.conflict_runtime_won));
        if (result.new_content != null) {
          openDraft(taskId, relativePath, {
            base: result.new_content,
            baseSha256: result.current_sha256 ?? baseSha256,
            edited: result.new_content,
          });
        }
        return;
      }
      // Clean apply (edit digest or merged content): clear the draft.
      discard(taskId, relativePath);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        // The base changed since the editor opened it; reload and rebase.
        setConflictMessage(t(($) => $.file_edit.changed_reload));
        onReload?.();
        return;
      }
      setConflictMessage(t(($) => $.file_edit.save_failed));
    }
  }, [apply, dirty, isRemote, taskId, relativePath, path, target, draft, baseContent, baseSha256, edited, onReload, openDraft, discard, t]);


  if (isBinary) {
    return (
      <div className="h-full overflow-y-auto p-6 text-body">
        <Alert className="mx-auto max-w-[68ch]">
          {t(($) => $.file_edit.binary_read_only)}
        </Alert>
      </div>
    );
  }

  return (
    <div className="relative flex h-full flex-col">
      <FileViewer
        path={relativePath}
        content={editing ? edited : baseContent}
        mode={mode}
        readOnly={fileReadOnly}
        onChange={onEditChange}
      />
      {conflictMessage && (
        <Alert variant="destructive" className="absolute inset-x-6 bottom-24 z-10">
          {conflictMessage}
        </Alert>
      )}
      <div className="flex items-center justify-end gap-2 border-t px-6 py-2">
        {!editing && canToggleEdit && (
          <Button variant="outline" onClick={onEditToggle}>
            {t(($) => $.file_edit.edit)}
          </Button>
        )}
        {editing && (
          <>
            <Button variant="ghost" onClick={() => { discard(taskId, relativePath); setEditing(false); }}>
              {t(($) => $.file_edit.discard)}
            </Button>
            <Button onClick={save} disabled={!dirty || apply.isPending}>
              {apply.isPending ? t(($) => $.file_edit.saving) : t(($) => $.file_edit.save)}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
