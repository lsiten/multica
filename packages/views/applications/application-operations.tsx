"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Loader2 } from "@multica/ui/icons";
import { applicationBoardOptions, applicationKeys, applicationOperationOptions, useApplicationOperationPanel, type ApplicationBoard, type ApplicationOperation } from "@multica/core/applications";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@multica/ui/components/ui/sheet";
import { AppLink } from "../navigation";
import { useLocale, useT } from "../i18n";
import { ApplicationStatusBadge } from "./controls";
import { ApplicationCancelOperation } from "./application-cancel-operation";
import { useApplicationActionLabels } from "./application-action-labels";

function OperationProgress({ operation, board, workspaceId }: { readonly operation: ApplicationOperation; readonly board: ApplicationBoard | undefined; readonly workspaceId: string }) {
  const { t } = useT("applications");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const labels = useApplicationActionLabels();
  const name = board?.applications.find((application) => application.id === operation.application_id)?.name;
  const completed = operation.steps.filter((step) => step.state === "completed").length;
  return <article className="space-y-3 border-b pb-6 last:border-b-0">
    <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><AppLink href={`${paths.applicationDetail(operation.application_id)}?tab=activity`} onClick={() => useApplicationOperationPanel.getState().close()} className="break-words text-body font-medium hover:underline">{name ?? t(($) => $.title)}</AppLink><p className="mt-1 text-caption text-muted-foreground">{operation.action === "unknown" ? t(($) => $.unknown) : labels[operation.action]}</p></div><ApplicationStatusBadge status={operation.state} /></div>
    <dl className="grid gap-2 text-caption text-muted-foreground"><div className="flex flex-wrap gap-2"><dt>{t(($) => $.operation_started)}</dt><dd>{new Date(operation.created_at).toLocaleString(locale)}</dd></div>{operation.deadline_at && <div className="flex flex-wrap gap-2"><dt>{t(($) => $.operation_deadline)}</dt><dd>{new Date(operation.deadline_at).toLocaleString(locale)}</dd></div>}{operation.completed_at && <div className="flex flex-wrap gap-2"><dt>{t(($) => $.completed)}</dt><dd>{new Date(operation.completed_at).toLocaleString(locale)}</dd></div>}</dl>
    {operation.sync_pending && <p role="status" className="text-caption text-warning">{t(($) => $.sync_pending)}</p>}
    {operation.steps.length > 0 && <><p className="text-caption tabular-nums">{t(($) => $.operation_progress, { completed, total: operation.steps.length })}</p><progress className="h-2 w-full accent-primary" value={completed} max={operation.steps.length} aria-label={t(($) => $.operation_steps)} /><ol className="space-y-3">{operation.steps.map((step) => <li key={step.id} className="space-y-1"><div className="flex flex-wrap items-center justify-between gap-2"><AppLink href={`${paths.applicationDetail(step.application_id)}?tab=logs`} onClick={() => useApplicationOperationPanel.getState().close()} className="min-w-0 break-words text-body hover:underline">{board?.applications.find((application) => application.id === step.application_id)?.name ?? t(($) => $.title)}</AppLink><ApplicationStatusBadge status={step.state} /></div><p className="text-caption text-muted-foreground">{t(($) => $.wave, { number: step.wave + 1 })}</p>{step.error && <p className="break-words text-caption text-destructive">{step.error}</p>}</li>)}</ol></>}
    {operation.error && <p role="alert" className="break-words text-body text-destructive">{operation.error}</p>}
    <ApplicationCancelOperation workspaceId={workspaceId} operation={operation} />
    {(operation.state === "failed" || operation.state === "partial" || operation.steps.some((step) => step.state === "blocked")) && <div className="space-y-2"><p className="text-caption text-muted-foreground">{t(($) => $.review_incomplete_help)}</p><AppLink href={`${paths.applicationDetail(operation.application_id)}?tab=activity`} onClick={() => useApplicationOperationPanel.getState().close()} className="text-body underline underline-offset-4">{t(($) => $.review_operation)}</AppLink></div>}
  </article>;
}

/** One monitor in the workspace shell; triggers can appear on any application surface. */
export function ApplicationOperationMonitor({ workspaceId }: { readonly workspaceId: string }) {
  const { t } = useT("applications");
  const open = useApplicationOperationPanel((state) => state.workspaceId === workspaceId);
  const focused = useApplicationOperationPanel((state) => state.workspaceId === workspaceId ? state.focused : null);
  const client = useQueryClient();
  const boardQuery = useQuery({ ...applicationBoardOptions(workspaceId), enabled: open });
  const cached = focused ? client.getQueryData<ApplicationOperation>(applicationKeys.operation(workspaceId, focused.applicationId, focused.operationId)) : undefined;
  const operationQuery = useQuery({ ...applicationOperationOptions(workspaceId, focused?.applicationId ?? "", focused?.operationId ?? ""), enabled: open && !!focused && cached?.sync_pending !== true });
  const boardOperation = focused ? boardQuery.data?.operations.find((operation) => operation.id === focused.operationId) : undefined;
  const latest = operationQuery.data?.sync_pending && boardOperation ? boardOperation : operationQuery.data ?? boardOperation;
  const operations = latest ? [latest, ...(boardQuery.data?.operations ?? []).filter((operation) => operation.id !== latest.id)] : boardQuery.data?.operations ?? [];
  return <Sheet open={open} onOpenChange={(next) => { if (!next) useApplicationOperationPanel.getState().close(); }}>
    <SheetContent className="gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-xl" aria-describedby={undefined}>
      <SheetHeader className="shrink-0 border-b pr-12"><SheetTitle>{t(($) => $.operations)}</SheetTitle></SheetHeader>
      <div data-application-operations-scroll className="min-h-0 flex-1 space-y-6 overflow-y-auto p-4">
        {(boardQuery.isError || operationQuery.isError) && <div role="alert" className="space-y-2"><p className="break-words text-body text-destructive">{boardQuery.error?.message ?? operationQuery.error?.message}</p><Button variant="outline" size="sm" onClick={() => { void boardQuery.refetch(); if (focused && !cached?.sync_pending) void operationQuery.refetch(); }}>{t(($) => $.retry)}</Button></div>}
        {boardQuery.isPending && !operations.length ? <p role="status" className="flex items-center gap-2 text-body"><Loader2 className="size-4 animate-spin" />{t(($) => $.status.checking)}</p> : !operations.length && <p className="text-body text-muted-foreground">{t(($) => $.no_activity)}</p>}
        {operations.map((operation) => <OperationProgress key={operation.id} operation={operation} board={boardQuery.data} workspaceId={workspaceId} />)}
      </div>
    </SheetContent>
  </Sheet>;
}

export function ApplicationOperations({ workspaceId }: { readonly workspaceId: string }) {
  const { t } = useT("applications");
  return <Button size="sm" variant="ghost" onClick={() => useApplicationOperationPanel.getState().open(workspaceId)}><Activity />{t(($) => $.operations)}</Button>;
}
