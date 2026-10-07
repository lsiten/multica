"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Edit3, Loader2, Play, RefreshCw, Server, Trash2, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { applicationBoardOptions, applicationDetailOptions, applicationServiceIds, useDeleteApplication, useLaunchApplication, type Application, type ApplicationBoard, type ApplicationInstance } from "@multica/core/applications";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeDisplayLabel, runtimeListOptions } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { AppLink, useNavigation } from "../navigation";
import { useT } from "../i18n";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { ApplicationEditor } from "./application-editor";
import { ApplicationOperationDialog, useApplicationActionLabels } from "./application-operation-dialog";
import { ApplicationRelations } from "./application-relations";
import { ApplicationStatusBadge } from "./controls";
import { ApplicationLogs } from "./application-logs";
import { ApplicationCancelOperation } from "./application-cancel-operation";

function InstanceCard({ instance, application, board, workspaceId, runtimeLabel }: { instance: ApplicationInstance; application: Application; board: ApplicationBoard; workspaceId: string; runtimeLabel: string }) {
  const { t } = useT("applications");
  const labels = useApplicationActionLabels();
  const paths = useWorkspacePaths();
  const child = board.applications.find((app) => app.id === instance.application_id) ?? application;
  const endpoints = board.endpoints.filter((endpoint) => endpoint.instance_id === instance.id);
  const external = child.config.mode === "external";
  const launch = useLaunchApplication(workspaceId, child.id);
  const [opening, setOpening] = useState(false);
  const openEndpoint = async (endpointId: string) => {
    const windowHandle = window.open("about:blank", "_blank");
    if (windowHandle) windowHandle.opener = null;
    setOpening(true);
    try { const url = await launch.mutateAsync(endpointId); if (windowHandle) windowHandle.location.href = url; else window.location.href = url; }
    catch (cause) { windowHandle?.close(); toast.error(cause instanceof Error ? cause.message : t(($) => $.load_failed)); }
    finally { setOpening(false); }
  };
  return <div className="space-y-4 rounded-xl border p-4">
    <div className="flex flex-wrap items-center justify-between gap-3"><div className="min-w-0"><h3 className="break-words text-body font-medium">{runtimeLabel}</h3>{child.id !== application.id && <AppLink href={paths.applicationDetail(child.id)} className="text-caption text-muted-foreground hover:underline">{child.name}</AppLink>}</div><ApplicationStatusBadge status={instance.status} /></div>
    <div className="grid gap-3 text-caption sm:grid-cols-3"><div><p className="mb-1 text-muted-foreground">{t(($) => $.process_state)}</p><ApplicationStatusBadge status={instance.process_state} /></div><div><p className="mb-1 text-muted-foreground">{t(($) => $.health_state)}</p><ApplicationStatusBadge status={instance.health_state} /></div><div><p className="mb-1 text-muted-foreground">{t(($) => $.connection_state)}</p><ApplicationStatusBadge status={instance.runtime_state} /></div></div>
    <div className="flex flex-wrap gap-x-5 gap-y-2 text-caption text-muted-foreground"><span>{t(($) => $.revision)} {instance.observed_revision || instance.revision}</span>{instance.code_version && <span className="font-mono">{t(($) => $.code_version)} {instance.code_version.slice(0, 12)}</span>}{instance.dirty && <span className="text-warning">{t(($) => $.uncommitted)}</span>}{instance.observed_at && <span>{t(($) => $.last_seen)} {new Date(instance.observed_at).toLocaleString()}</span>}</div>
    {instance.revision !== child.revision && <p className="text-caption text-warning">{t(($) => $.pending_revision)}</p>}
    {instance.error && <p role="status" className="break-words text-caption text-destructive">{instance.error}</p>}
    {endpoints.map((endpoint) => <div key={endpoint.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-muted/50 p-3"><span className="text-caption">{endpoint.state === "published" ? t(($) => $.published) : t(($) => $.unpublished)} · {endpoint.port}{endpoint.entry_path}</span><div className="flex gap-2">{endpoint.state === "published" && <Button variant="outline" size="xs" disabled={opening || instance.status !== "running"} onClick={() => void openEndpoint(endpoint.id)}>{opening && <Loader2 className="animate-spin" />}{t(($) => $.open)}</Button>}{endpoint.can_manage && endpoint.state === "published" && <ApplicationOperationDialog workspaceId={workspaceId} application={child} board={board} action="unpublish" initialRuntimeId={instance.runtime_id} trigger={<Button size="xs" variant="ghost"><X />{labels.unpublish}</Button>} />}</div></div>)}
    {instance.can_manage && <div className="flex flex-wrap gap-2">
      {instance.desired_state === "stopped" ? <ApplicationOperationDialog workspaceId={workspaceId} application={child} board={board} action="start" initialRuntimeId={instance.runtime_id} trigger={<Button size="sm" variant="outline"><Play />{external ? t(($) => $.check_service) : labels.start}</Button>} /> : <ApplicationOperationDialog workspaceId={workspaceId} application={child} board={board} action="stop" initialRuntimeId={instance.runtime_id} trigger={<Button size="sm" variant="outline">{external ? t(($) => $.stop_monitor) : labels.stop}</Button>} />}
      {!external && <ApplicationOperationDialog workspaceId={workspaceId} application={child} board={board} action="restart" initialRuntimeId={instance.runtime_id} trigger={<Button size="sm" variant="outline"><RefreshCw />{labels.restart}</Button>} />}
      {child.config.port > 0 && instance.desired_state === "running" && <ApplicationOperationDialog workspaceId={workspaceId} application={child} board={board} action="publish" initialRuntimeId={instance.runtime_id} trigger={<Button size="sm" variant="outline"><Upload />{labels.publish}</Button>} />}
    </div>}
  </div>;
}

function DeleteApplication({ workspaceId, application }: { workspaceId: string; application: Application }) {
  const { t } = useT("applications");
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const remove = useDeleteApplication(workspaceId);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const confirm = async () => {
    try { setError(""); await remove.mutateAsync({ id: application.id, revision: application.revision }); setOpen(false); toast.success(t(($) => $.deleted)); navigation.push(paths.applications()); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.invalid_config)); }
  };
  return <AlertDialog open={open} onOpenChange={setOpen}><AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t(($) => $.delete)}><Trash2 /></Button>} /><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t(($) => $.delete)}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.delete_help)}</AlertDialogDescription></AlertDialogHeader>{error && <p role="alert" className="break-words text-caption text-destructive">{error}</p>}<AlertDialogFooter><AlertDialogCancel disabled={remove.isPending}>{t(($) => $.cancel)}</AlertDialogCancel><Button variant="destructive" disabled={remove.isPending} aria-busy={remove.isPending} onClick={() => void confirm()}>{t(($) => $.delete)}</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>;
}

export function ApplicationDetail({ applicationId }: { applicationId: string }) {
  const { t } = useT("applications");
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const applicationQuery = useQuery(applicationDetailOptions(workspaceId, applicationId));
  const boardQuery = useQuery(applicationBoardOptions(workspaceId));
  const runtimes = useQuery(runtimeListOptions(workspaceId));
  const [tab, setTab] = useState<"instances" | "configuration" | "orchestration" | "activity" | "logs">("instances");
  const application = applicationQuery.data;
  const board = boardQuery.data;
  const labels = useApplicationActionLabels();
  if (applicationQuery.isPending || boardQuery.isPending) return <div role="status" className="flex flex-1 justify-center py-24"><Loader2 className="size-6 animate-spin text-muted-foreground" /></div>;
  if (!application || !board) return <CollectionPageState icon={Server} title={t(($) => $.load_failed)} description={applicationQuery.error?.message ?? boardQuery.error?.message} role="alert" actions={<Button variant="outline" onClick={() => { void applicationQuery.refetch(); void boardQuery.refetch(); }}>{t(($) => $.retry)}</Button>} />;
  const ids = applicationServiceIds(application, board.applications);
  const instances = board.instances.filter((instance) => ids.has(instance.application_id));
  const operations = board.operations.filter((operation) => operation.application_id === applicationId);
  const names = new Map(board.applications.map((app) => [app.id, app.name]));
  const runtimeById = new Map((runtimes.data ?? []).map((runtime) => [runtime.id, runtime]));
  const tabs = [{ value: "instances", label: t(($) => $.instances) }, { value: "configuration", label: t(($) => $.configuration) }, { value: "orchestration", label: t(($) => $.orchestration) }, { value: "activity", label: t(($) => $.activity) }, { value: "logs", label: t(($) => $.logs) }] as const;

  return <div className="flex min-h-0 flex-1 flex-col">
    <CollectionPageHeader icon={Server} title={application.name} actions={<><ApplicationEditor workspaceId={workspaceId} application={application} trigger={<Button size="sm" variant="outline"><Edit3 />{t(($) => $.edit)}</Button>} /><DeleteApplication workspaceId={workspaceId} application={application} /></>} />
    <div className={cn("shrink-0 border-b", PAGE_GUTTER)}><div className={cn("flex flex-wrap items-center gap-2 py-3", PAGE_RAIL)}><AppLink href={paths.applications()} className="mr-3 flex items-center gap-1 text-caption text-muted-foreground hover:text-foreground"><ArrowLeft className="size-3.5" />{t(($) => $.title)}</AppLink><div role="group" aria-label={t(($) => $.title)} className="flex flex-wrap gap-1">{tabs.map((entry) => <Button key={entry.value} size="sm" variant={tab === entry.value ? "secondary" : "ghost"} aria-pressed={tab === entry.value} onClick={() => setTab(entry.value)}>{entry.label}</Button>)}</div></div></div>
    <div className={cn("min-h-0 flex-1 overflow-y-auto py-6", PAGE_GUTTER)}><div className={cn("space-y-6", PAGE_RAIL)}>
      {tab === "instances" && <><div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-body font-medium">{t(($) => $.instances)} <span className="ml-1 text-muted-foreground">{instances.length}</span></h2><div className="flex flex-wrap gap-2"><ApplicationOperationDialog workspaceId={workspaceId} application={application} board={board} action="start" trigger={<Button size="sm"><Play />{application.config.mode === "external" ? t(($) => $.check_service) : labels.start}</Button>} />{application.kind === "composition" && instances.length > 0 && <ApplicationOperationDialog workspaceId={workspaceId} application={application} board={board} action="stop" trigger={<Button size="sm" variant="outline">{labels.stop}</Button>} />}</div></div>{!instances.length && <p className="rounded-xl border p-8 text-center text-body text-muted-foreground">{t(($) => $.no_instances)}</p>}<div className="grid gap-4 xl:grid-cols-2">{instances.map((instance) => { const runtime = runtimeById.get(instance.runtime_id); return <InstanceCard key={instance.id} workspaceId={workspaceId} application={application} board={board} instance={instance} runtimeLabel={runtime ? runtimeDisplayLabel(runtime) : t(($) => $.private_runtime)} />; })}</div></>}
      {tab === "configuration" && <section className="max-w-3xl space-y-5 rounded-xl border p-5"><div className="flex items-center justify-between"><h2 className="text-body font-medium">{t(($) => $.configuration)}</h2><span className="text-caption text-muted-foreground">{t(($) => $.revision)} {application.revision}</span></div><dl className="grid gap-4 text-body sm:grid-cols-2"><div><dt className="text-caption text-muted-foreground">{t(($) => $.kind)}</dt><dd className="mt-1">{application.kind === "composition" ? t(($) => $.composition) : t(($) => $.service)}</dd></div>{application.kind === "service" && <><div><dt className="text-caption text-muted-foreground">{t(($) => $.mode)}</dt><dd className="mt-1">{application.config.mode === "external" ? t(($) => $.external) : t(($) => $.managed)}</dd></div><div><dt className="text-caption text-muted-foreground">{t(($) => $.port)}</dt><dd className="mt-1 font-mono">{application.config.port}</dd></div><div><dt className="text-caption text-muted-foreground">{t(($) => $.health)}</dt><dd className="mt-1">{application.config.health.kind === "http" ? t(($) => $.health_http) : application.config.health.kind === "tcp" ? t(($) => $.health_tcp) : t(($) => $.health_none)}</dd></div><div className="sm:col-span-2"><dt className="text-caption text-muted-foreground">{t(($) => $.program)}</dt><dd className="mt-1 overflow-x-auto rounded-lg bg-muted p-3 font-mono text-caption">{JSON.stringify(application.config.command)}</dd></div>{application.config.work_dir && <div><dt className="text-caption text-muted-foreground">{t(($) => $.work_dir)}</dt><dd className="mt-1 break-words font-mono text-caption">{application.config.work_dir}</dd></div>}{application.config.ref && <div><dt className="text-caption text-muted-foreground">{t(($) => $.ref)}</dt><dd className="mt-1 break-words font-mono text-caption">{application.config.ref}</dd></div>}</>}</dl></section>}
      {tab === "orchestration" && <ApplicationRelations key={application.id} workspaceId={workspaceId} application={application} board={board} />}
      {tab === "logs" && <ApplicationLogs key={application.id} workspaceId={workspaceId} application={application} board={board} />}
      {tab === "activity" && <section className="space-y-4"><h2 className="text-body font-medium">{t(($) => $.activity)}</h2>{!operations.length && <p className="rounded-xl border p-8 text-center text-caption text-muted-foreground">{t(($) => $.no_activity)}</p>}{operations.map((operation) => <details key={operation.id} className="rounded-xl border p-4" open={operation.state === "running" || operation.state === "queued" || operation.state === "cancelling"}><summary className="cursor-pointer"><span className="inline-flex flex-wrap items-center gap-3 text-body"><span>{operation.action === "unknown" ? t(($) => $.unknown) : labels[operation.action]}</span><ApplicationStatusBadge status={operation.state} /><span className="text-caption text-muted-foreground">{new Date(operation.created_at).toLocaleString()}</span><span className="text-caption text-muted-foreground">{operation.actor_type}</span></span></summary><ApplicationCancelOperation workspaceId={workspaceId} operation={operation} />{operation.error && <p className="mt-3 break-words text-caption text-destructive">{operation.error}</p>}<div className="mt-3 space-y-2">{operation.steps.map((step) => <div key={step.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-muted/40 p-3"><span className="min-w-0 break-words text-caption">{names.get(step.application_id) ?? step.application_id}</span><ApplicationStatusBadge status={step.state} />{step.error && <p className="w-full break-words text-caption text-destructive">{step.error}</p>}</div>)}</div></details>)}</section>}
    </div></div>
  </div>;
}
