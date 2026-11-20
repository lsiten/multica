"use client";

import { useState } from "react";
import { Loader2, RefreshCw, Square, X } from "@multica/ui/icons";
import { ApplicationBulkOperationError, useApplicationBulkOperation, type ApplicationBulkOperationItem } from "@multica/core/applications";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { useT } from "../i18n";
import { toast } from "sonner";

export function ApplicationBulkActions({ workspaceId, items, onClear }: { readonly workspaceId: string; readonly items: readonly ApplicationBulkOperationItem[]; readonly onClear: () => void }) {
  const { t } = useT("applications");
  const operation = useApplicationBulkOperation(workspaceId);
  const [action, setAction] = useState<"start" | "stop" | "restart" | null>(null);
  const [force, setForce] = useState(false);
  const submit = async () => {
    if (!action) return;
    try {
      const result = await operation.mutateAsync({ action, items, force });
      toast.success(t(($) => $.bulk_accepted, { succeeded: result.succeeded.length, failed: result.failed.length }));
      setAction(null);
      setForce(false);
      onClear();
    } catch (cause) {
      const result = cause instanceof ApplicationBulkOperationError ? cause.result : null;
      toast.error(result ? t(($) => $.bulk_failed, { failed: result.failed.length }) : cause instanceof Error ? cause.message : t(($) => $.load_failed));
    }
  };
  return <>
    <div className="sticky bottom-3 z-10 flex flex-wrap items-center gap-2 rounded-xl border bg-surface-raised/95 p-2 shadow-[var(--floating-shadow)] backdrop-blur-sm"><span className="px-2 text-caption font-medium">{t(($) => $.bulk_selected, { count: items.length })}</span><Button size="sm" variant="outline" onClick={() => setAction("start")} disabled={operation.isPending}><RefreshCw />{t(($) => $.start)}</Button><Button size="sm" variant="outline" onClick={() => { setForce(false); setAction("restart"); }} disabled={operation.isPending}><RefreshCw />{t(($) => $.restart)}</Button><Button size="sm" variant="outline" onClick={() => { setForce(false); setAction("stop"); }} disabled={operation.isPending}><Square />{t(($) => $.stop)}</Button><Button size="sm" variant="ghost" onClick={onClear} disabled={operation.isPending}><X />{t(($) => $.cancel)}</Button>{operation.isPending && <Loader2 className="size-4 animate-spin text-muted-foreground" />}</div>
    <AlertDialog open={action !== null} onOpenChange={(open) => { if (!open && !operation.isPending) setAction(null); }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t(($) => $.bulk_confirm_title, { action: action === "start" ? t(($) => $.start) : action === "restart" ? t(($) => $.restart) : t(($) => $.stop), count: items.length })}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.bulk_confirm_help)}</AlertDialogDescription></AlertDialogHeader>{(action === "stop" || action === "restart") && <label className="flex items-center justify-between gap-4 rounded-lg border p-3 text-caption"><span>{t(($) => $.force)}</span><Checkbox checked={force} onCheckedChange={(checked) => setForce(checked === true)} /></label>}<AlertDialogFooter><AlertDialogCancel disabled={operation.isPending}>{t(($) => $.cancel)}</AlertDialogCancel><AlertDialogAction disabled={operation.isPending} onClick={(event) => { event.preventDefault(); void submit(); }}>{operation.isPending && <Loader2 className="animate-spin" />}{t(($) => $.bulk_confirm)}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </>;
}
