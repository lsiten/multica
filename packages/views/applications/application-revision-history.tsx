"use client";

import { useMemo, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Check, ChevronDown, Loader2, RotateCcw } from "@multica/ui/icons";
import { applicationRevisionDiff, applicationRevisionHistoryOptions, applicationRevisionOptions, type Application, type ApplicationBoard, type ApplicationRevision, type ApplicationRevisionScope, useRestoreApplicationRevision } from "@multica/core/applications";
import { Button } from "@multica/ui/components/ui/button";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { useLocale, useT } from "../i18n";
import { ApplicationOperationDialog } from "./application-operation-dialog";
import { toast } from "sonner";

function RevisionRow({ revision, selected, onSelect }: { readonly revision: ApplicationRevision; readonly selected: boolean; readonly onSelect: () => void }) {
  const { t } = useT("applications");
  const locale = useLocale();
  return <button type="button" onClick={onSelect} aria-pressed={selected} className={cn("flex w-full items-start justify-between gap-3 rounded-lg px-3 py-3 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", selected && "bg-surface-selected")}>
    <span className="min-w-0"><span className="flex items-center gap-2 text-body font-medium"><span>{t(($) => $.revision)} {revision.revision}</span>{selected && <Check aria-hidden className="size-4 shrink-0 text-foreground" />}</span><span className="mt-1 block text-caption text-muted-foreground">{new Date(revision.created_at).toLocaleString(locale)} · {revision.actor_type === "agent" ? t(($) => $.actor_types.agent) : revision.actor_type === "member" ? t(($) => $.actor_types.member) : t(($) => $.actor_types.unknown)}</span></span><ChevronDown className={cn("size-4 shrink-0 text-muted-foreground transition-transform", selected && "rotate-[-90deg]")} />
  </button>;
}

function DiffPanel({ before, after }: { readonly before: ApplicationRevision; readonly after: ApplicationRevision }) {
  const { t } = useT("applications");
  const changes = useMemo(() => applicationRevisionDiff(before, after), [before, after]);
  if (!changes.length) return <p className="rounded-lg bg-muted/40 p-3 text-caption text-muted-foreground">{t(($) => $.revision_no_changes)}</p>;
  return <div className="space-y-2">{changes.map((change) => <div key={change.path} className="grid gap-2 rounded-lg bg-muted/40 p-3 text-caption sm:grid-cols-[minmax(9rem,0.8fr)_minmax(0,1fr)_minmax(0,1fr)]"><span className="break-words font-mono text-muted-foreground">{change.path}</span><span className="min-w-0 break-words text-destructive">{change.before ?? t(($) => $.revision_missing)}</span><span className="min-w-0 break-words text-success">{change.after ?? t(($) => $.revision_missing)}</span></div>)}</div>;
}

export function ApplicationRevisionHistory({ workspaceId, application, board }: { readonly workspaceId: string; readonly application: Application; readonly board: ApplicationBoard }) {
  const { t } = useT("applications");
  const scope: ApplicationRevisionScope = { workspaceId, applicationId: application.id };
  const history = useInfiniteQuery(applicationRevisionHistoryOptions(scope, application.revision));
  const revisions = history.data?.pages.flatMap((page) => page.revisions) ?? [];
  const [selectedRevision, setSelectedRevision] = useState(application.revision);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [restored, setRestored] = useState<Application | null>(null);
  const selected = revisions.find((revision) => revision.revision === selectedRevision);
  const current = useQuery(applicationRevisionOptions(scope, application.revision));
  const restore = useRestoreApplicationRevision(scope);
  const affected = board.instances.filter((instance) => instance.revision !== application.revision);
  const restoreSelected = async () => {
    if (!selected || selected.revision === application.revision) return;
    try { const next = await restore.mutateAsync({ revision: application.revision, target_revision: selected.revision }); setRestored(next); setRestoreOpen(false); toast.success(t(($) => $.revision_restored, { revision: next.revision })); }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : t(($) => $.load_failed)); }
  };
  return <section className="space-y-4 rounded-xl border p-5">
    <div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-body font-medium">{t(($) => $.revision_history)}</h3><p className="mt-1 text-caption text-muted-foreground">{t(($) => $.revision_history_help)}</p></div>{history.isError && <Button size="sm" variant="outline" onClick={() => void history.refetch()}>{t(($) => $.retry)}</Button>}</div>
    {history.isPending ? <p role="status" className="flex items-center gap-2 text-caption text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t(($) => $.status.checking)}</p> : <div className="grid gap-4 lg:grid-cols-[minmax(14rem,0.7fr)_minmax(0,1.3fr)]">
      <div className="space-y-1">{revisions.map((revision) => <RevisionRow key={revision.revision} revision={revision} selected={revision.revision === selectedRevision} onSelect={() => setSelectedRevision(revision.revision)} />)}{history.hasNextPage && <Button variant="ghost" size="sm" disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>{history.isFetchingNextPage && <Loader2 className="animate-spin" />}{t(($) => $.load_more)}</Button>}{!revisions.length && <p className="p-3 text-caption text-muted-foreground">{t(($) => $.no_revisions)}</p>}</div>
      <div className="min-w-0 space-y-4">{selected && current.data ? <><div className="flex flex-wrap items-center justify-between gap-3"><p className="text-caption text-muted-foreground">{t(($) => $.revision_compare, { revision: selected.revision, current: application.revision })}</p>{selected.revision !== application.revision && <Button size="sm" variant="outline" onClick={() => setRestoreOpen(true)} disabled={restore.isPending}><RotateCcw />{t(($) => $.restore_revision)}</Button>}</div><DiffPanel before={selected} after={current.data} /></> : <p className="text-caption text-muted-foreground">{t(($) => $.select_revision)}</p>}
        {restored && affected.length > 0 && <div className="space-y-3 rounded-lg border border-warning/40 bg-warning/10 p-3"><p className="text-body font-medium">{t(($) => $.revision_restart_help, { count: affected.length })}</p><div className="flex flex-wrap gap-2">{affected.slice(0, 3).map((instance) => <ApplicationOperationDialog key={instance.id} workspaceId={workspaceId} application={restored} board={board} action="restart" initialRuntimeId={instance.runtime_id} trigger={<Button size="sm" variant="outline">{t(($) => $.restart)} · {instance.runtime_id.slice(0, 8)}</Button>} />)}</div></div>}
      </div>
    </div>}
    <AlertDialog open={restoreOpen} onOpenChange={setRestoreOpen}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t(($) => $.restore_revision)} · {t(($) => $.revision)} {selectedRevision}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.restore_revision_help)}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel disabled={restore.isPending}>{t(($) => $.cancel)}</AlertDialogCancel><AlertDialogAction disabled={restore.isPending} onClick={() => void restoreSelected()}>{restore.isPending && <Loader2 className="animate-spin" />}{t(($) => $.restore_revision)}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </section>;
}
