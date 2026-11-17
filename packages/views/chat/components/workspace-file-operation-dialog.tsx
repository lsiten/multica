"use client";

import { useId, useState } from "react";
import { applyWorkspaceFileOperationResult, useWorkspaceFileOperationStore, workspaceFileOperationScope, type PendingWorkspaceFileOperation, type WorkspaceFileOperationContext } from "@multica/core/chat";
import { workspaceFileOperationSchema, type WorkspaceFileOperation } from "@multica/core/types/workspace-file-operation";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@multica/ui/components/ui/dialog";
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter } from "@multica/ui/components/ui/alert-dialog";
import { Loader2 } from "@multica/ui/icons";
import { useT } from "../../i18n";
import { useWorkspaceFileOperation } from "./use-workspace-file-operation";

export type WorkspaceFileChoice = { readonly pending: PendingWorkspaceFileOperation } | {
  readonly context: WorkspaceFileOperationContext;
  readonly operation: Extract<WorkspaceFileOperation, { kind: "move" | "trash" | "restore" }>;
  readonly directory: boolean;
};

export function WorkspaceFileOperationDialog({ choice, onClose }: { readonly choice: WorkspaceFileChoice; readonly onClose: () => void }) {
  const { t } = useT("chat");
  const context = "pending" in choice ? choice.pending.context : choice.context;
  const initial = "pending" in choice ? choice.pending.operation : choice.operation;
  const scope = workspaceFileOperationScope(context);
  const pending = useWorkspaceFileOperationStore((state) => state.pending[scope]);
  const operation = pending?.operation ?? initial;
  const [destination, setDestination] = useState(initial.kind === "move" ? initial.destination : initial.path);
  const mutation = useWorkspaceFileOperation();
  const pathId = useId();
  const candidate = workspaceFileOperationSchema.safeParse(operation.kind === "move" ? { ...operation, destination } : operation.kind === "restore" ? { ...operation, path: destination } : operation);
  const submit = async () => {
    if (mutation.isPending || (!pending && !candidate.success)) return;
    const command = pending ?? (candidate.success ? useWorkspaceFileOperationStore.getState().prepare(context, candidate.data) : null);
    if (!command) return;
    try {
      const result = await mutation.mutateAsync(command);
      applyWorkspaceFileOperationResult(result);
    }
    catch (error) { if (!(error instanceof Error)) throw error; return; }
    onClose();
  };
  const label = t(($) => $.file_operations[operation.kind]);
  const error = mutation.isError && <div role="alert" className="space-y-2 text-body text-destructive">
    <p>{t(($) => pending ? $.file_operations.unconfirmed : $.file_operations.failed)}</p>
    <details className="text-caption text-muted-foreground"><summary>{t(($) => $.doc_file_list.error_details)}</summary><p className="break-words">{mutation.error.message}</p></details>
  </div>;
  const close = <Button type="button" variant="outline" disabled={mutation.isPending} onClick={onClose}>{t(($) => $.file_creation.close)}</Button>;
  const confirm = <Button type="button" variant={operation.kind === "trash" ? "destructive" : "default"} disabled={mutation.isPending || (!pending && !candidate.success)} onClick={() => void submit()}>
    {mutation.isPending && <Loader2 className="animate-spin motion-reduce:animate-none" />}{pending ? t(($) => $.doc_file_list.retry) : label}
  </Button>;
  const location = <dl className="space-y-1 text-caption"><dt className="text-muted-foreground">{t(($) => $.file_operations.context)}</dt><dd className="break-all font-mono" dir="ltr">{context.path}</dd></dl>;
  if (operation.kind === "trash") return <AlertDialog open onOpenChange={(open, details) => { if (mutation.isPending) details.cancel(); else if (!open) onClose(); }}>
    <AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{label}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.file_operations.trash_hint)}</AlertDialogDescription></AlertDialogHeader>
      <p className="break-all text-body" dir="ltr">{operation.path}</p>
      {"directory" in choice && choice.directory && <p className="text-caption text-muted-foreground">{t(($) => $.file_operations.folder_hint)}</p>}
      {location}{error}<AlertDialogFooter>{close}{confirm}</AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>;
  const editable = operation.kind === "move" || operation.kind === "restore";
  return <Dialog open onOpenChange={(open, details) => { if (mutation.isPending) details.cancel(); else if (!open) onClose(); }}>
    <DialogContent showCloseButton={!mutation.isPending}>
      <form className="min-w-0 space-y-4" onSubmit={(event) => { event.preventDefault(); void submit(); }} aria-busy={mutation.isPending}>
        <DialogHeader><DialogTitle>{label}</DialogTitle>{operation.kind === "restore" && <DialogDescription>{t(($) => $.file_operations.restore_hint)}</DialogDescription>}</DialogHeader>
        {location}<dl className="space-y-1 text-body"><dt className="text-caption text-muted-foreground">{t(($) => $.file_operations.source)}</dt><dd className="break-all font-mono" dir="ltr">{operation.path}</dd></dl>
        {editable && <div className="space-y-2"><Label htmlFor={pathId}>{t(($) => $.file_operations.destination)}</Label><Input id={pathId} value={pending ? (operation.kind === "move" ? operation.destination : operation.path) : destination} onChange={(event) => setDestination(event.target.value)} disabled={!!pending || mutation.isPending} autoFocus dir="ltr" />
          {!pending && !candidate.success && !(operation.kind === "move" && destination === operation.path) && <p className="text-caption text-muted-foreground">{t(($) => $.file_creation.invalid_path)}</p>}
        </div>}
        {error}<DialogFooter>{close}{confirm}</DialogFooter>
      </form>
    </DialogContent>
  </Dialog>;
}
