"use client";

import { useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceFileOperationCapabilitiesOptions } from "@multica/core/chat/queries";
import { Loader2, Plus } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { useWorkspaceFileOperationStore, workspaceFileOperationScope, type WorkspaceFileOperationContext } from "@multica/core/chat";
import { workspaceFileCreationSchema, type WorkspaceFileOperationResult } from "@multica/core/types/workspace-file-operation";
import { useT } from "../../i18n";
import { useWorkspaceFileOperation } from "./use-workspace-file-operation";

export function WorkspaceFileCreate({ context, supported, onCreated }: {
  readonly context: WorkspaceFileOperationContext;
  readonly supported: boolean;
  readonly onCreated: (result: WorkspaceFileOperationResult) => void;
}) {
  const { t } = useT("chat");
  const capability = useQuery(workspaceFileOperationCapabilitiesOptions());
  const scope = workspaceFileOperationScope(context);
  const current = useWorkspaceFileOperationStore((state) => state.pending[scope]);
  const pending = current?.operation;
  const creation = pending?.kind === "create_file" || pending?.kind === "create_directory" ? pending : undefined;
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState(creation?.path ?? "");
  const [kind, setKind] = useState<"create_file" | "create_directory">(creation?.kind ?? "create_file");
  const mutation = useWorkspaceFileOperation();
  const inputId = useId();
  const typeId = useId();
  const helpId = useId();
  const operation = workspaceFileCreationSchema.safeParse({ kind, path });
  const submit = async () => {
    if (!operation.success || mutation.isPending) return;
    const command = useWorkspaceFileOperationStore.getState().prepare(context, operation.data);
    let result: WorkspaceFileOperationResult;
    try {
      result = await mutation.mutateAsync(command);
    } catch (error) {
      if (!(error instanceof Error)) throw error;
      // The mutation owns the error; keep the dialog and command for retry.
      return;
    }
    onCreated(result);
    setOpen(false);
    setPath("");
  };
  if ((pending && !creation) || (!pending && (!supported || capability.data?.workspace_file_operations_supported !== true))) return null;
  return <Dialog open={open} onOpenChange={(next, details) => {
    if (mutation.isPending) { details.cancel(); return; }
    if (next) {
      const current = useWorkspaceFileOperationStore.getState().pending[scope];
      if (current?.operation.kind === "create_file" || current?.operation.kind === "create_directory") { setPath(current.operation.path); setKind(current.operation.kind); }
      mutation.reset();
    }
    setOpen(next);
  }}>
    <DialogTrigger render={<Button variant="ghost" size="sm" />}><Plus />{t(($) => pending ? $.file_creation.resume : $.file_creation.new)}</DialogTrigger>
    <DialogContent showCloseButton={!mutation.isPending}>
      <form className="min-w-0 space-y-4" onSubmit={(event) => { event.preventDefault(); void submit(); }} aria-busy={mutation.isPending}>
        <DialogHeader>
          <DialogTitle>{t(($) => $.file_creation.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.file_creation.location_hint)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor={typeId}>{t(($) => $.file_creation.type)}</Label>
          <select id={typeId} value={kind} disabled={!!pending || mutation.isPending} onChange={(event) => setKind(event.target.value === "create_directory" ? "create_directory" : "create_file")} className="h-9 w-full rounded-md border bg-background px-3 text-body focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <option value="create_file">{t(($) => $.file_creation.file)}</option>
            <option value="create_directory">{t(($) => $.file_creation.folder)}</option>
          </select>
        </div>
        <div className="space-y-2">
          <Label htmlFor={inputId}>{t(($) => $.file_creation.path)}</Label>
          <Input id={inputId} value={path} disabled={!!pending || mutation.isPending} onChange={(event) => setPath(event.target.value)} aria-invalid={!!path && !operation.success} aria-describedby={!!path && !operation.success ? helpId : undefined} autoComplete="off" spellCheck={false} />
          {!!path && !operation.success && <p id={helpId} className="text-caption text-destructive">{t(($) => $.file_creation.invalid_path)}</p>}
        </div>
        {pending && !mutation.isPending && <p role="status" className="text-body text-muted-foreground">{t(($) => $.file_creation.unconfirmed)}</p>}
        {mutation.error instanceof Error && <div role="alert" className="space-y-1">
          <p className="text-body text-destructive">{t(($) => $.file_creation.failed)}</p>
          <details className="text-caption text-muted-foreground"><summary className="cursor-pointer">{t(($) => $.doc_file_list.error_details)}</summary><p className="break-words">{mutation.error.message}</p></details>
        </div>}
        <DialogFooter>
          <Button type="button" variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>{t(($) => $.file_creation.close)}</Button>
          <Button type="submit" className="min-w-24" disabled={!operation.success || mutation.isPending}>
            {mutation.isPending && <Loader2 className="animate-spin motion-reduce:animate-none" />}{t(($) => pending ? $.doc_file_list.retry : $.file_creation.create)}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>;
}
