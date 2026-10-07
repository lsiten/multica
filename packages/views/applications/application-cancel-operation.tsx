"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import { useCancelApplicationOperation, type ApplicationOperation } from "@multica/core/applications";
import { Button } from "@multica/ui/components/ui/button";
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../i18n";

export function ApplicationCancelOperation({ workspaceId, operation }: { workspaceId: string; operation: ApplicationOperation }) {
  const { t } = useT("applications");
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const cancel = useCancelApplicationOperation(workspaceId, operation.application_id);
  if (operation.state !== "queued" && operation.state !== "running") return null;
  async function confirm() {
    setError("");
    try { await cancel.mutateAsync(operation.id); setOpen(false); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.load_failed)); }
  }
  return <AlertDialog open={open} onOpenChange={setOpen}>
    <AlertDialogTrigger render={<Button variant="outline" size="xs" className="mt-3">{t(($) => $.cancel_operation)}</Button>} />
    <AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{t(($) => $.cancel_operation)}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.cancel_operation_help)}</AlertDialogDescription></AlertDialogHeader>
      {error && <p role="alert" className="break-words text-caption text-destructive">{error}</p>}
      <AlertDialogFooter><AlertDialogCancel disabled={cancel.isPending}>{t(($) => $.keep_operation)}</AlertDialogCancel><Button variant="destructive" disabled={cancel.isPending} aria-busy={cancel.isPending} onClick={() => void confirm()}>{cancel.isPending && <Loader2 className="animate-spin" />}{t(($) => $.cancel_operation)}</Button></AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>;
}
