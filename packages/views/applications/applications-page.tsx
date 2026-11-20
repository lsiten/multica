"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Activity, Check, FolderKanban, Loader2, Plus, RefreshCw, Search, Server } from "@multica/ui/icons";
import { applicationBoardOptions, applicationKindFilterSchema, applicationPublicationFilterSchema, applicationStatusFilterSchema, selectApplicationBoard, type ApplicationBulkOperationItem } from "@multica/core/applications";
import { projectListOptions } from "@multica/core/projects";
import { runtimeDisplayLabel, runtimeListOptions } from "@multica/core/runtimes";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { useNavigation, useSearchState } from "../navigation";
import { useT } from "../i18n";
import { CollectionCreateButton, CollectionNoMatches, CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { ApplicationEditor } from "./application-editor";
import { applicationViewFromParam } from "./application-view-state";
import { ApplicationSelect } from "./controls";
import { ApplicationResults } from "./application-results";
import { ApplicationOperations } from "./application-operations";
import { ApplicationBulkActions } from "./application-bulk-actions";

export function ApplicationsPage({ projectId: presetProjectId, runtimeId: presetRuntimeId, runtimeIds: presetRuntimeIds, embedded = false }: { projectId?: string; runtimeId?: string; runtimeIds?: string[]; embedded?: boolean }) {
  const { t } = useT("applications");
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const boardQuery = useQuery(applicationBoardOptions(workspaceId));
  const projects = useQuery(projectListOptions(workspaceId));
  const runtimes = useQuery(runtimeListOptions(workspaceId));
  const [viewParam, setView] = useSearchState("view", presetRuntimeId || presetRuntimeIds ? "runtimes" : "projects");
  const view = applicationViewFromParam(viewParam);
  const [search, setSearch] = useSearchState("q");
  const [projectParam, setProject] = useSearchState("project", "all");
  const [runtimeParam, setRuntime] = useSearchState("runtime", "all");
  const [statusParam, setStatus] = useSearchState("status", "all");
  const [kindParam, setKind] = useSearchState("kind", "all");
  const [publicationParam, setPublication] = useSearchState("publication", "all");
  const [selectedInstances, setSelectedInstances] = useState<Record<string, ApplicationBulkOperationItem>>({});
  const status = applicationStatusFilterSchema.parse(statusParam);
  const kind = applicationKindFilterSchema.parse(kindParam);
  const publication = applicationPublicationFilterSchema.parse(publicationParam);
  const projectId = presetProjectId ?? projectParam;
  const runtimeIds = presetRuntimeIds ?? (presetRuntimeId ? [presetRuntimeId] : runtimeParam !== "all" ? [runtimeParam] : null);
  const board = boardQuery.data;
  const selected = board ? selectApplicationBoard(board, { projectId, runtimeIds, search, status, kind, publication }) : undefined;
  const hasFilters = projectParam !== "all" || runtimeParam !== "all" || status !== "all" || kind !== "all" || publication !== "all";
  const reset = () => { setSearch(""); setProject("all"); setRuntime("all"); setStatus("all"); setKind("all"); setPublication("all"); };
  const selectedInstanceIds = new Set(Object.keys(selectedInstances));
  const toggleInstance = (instanceId: string, item: ApplicationBulkOperationItem) => setSelectedInstances((current) => { const next = { ...current }; if (next[instanceId]) delete next[instanceId]; else next[instanceId] = item; return next; });
  const createProjectId = projectId !== "all" ? projectId : projects.data?.length === 1 ? projects.data[0]?.id : undefined;
  const createApplication = (trigger: React.ReactElement) => <ApplicationEditor workspaceId={workspaceId} projectId={createProjectId} trigger={trigger} onSaved={(application) => navigation.push(paths.applicationDetail(application.id))} />;
  const filterSummary = (nextKind: string, nextStatus: string, nextView: string) => { setKind(nextKind); setStatus(nextStatus); setPublication("all"); setView(nextView); };
  const summaries = selected ? [
    { label: t(($) => $.services_count), value: selected.summary.services, active: kind === "service" && status === "all", select: () => filterSummary("service", "all", "projects") },
    { label: t(($) => $.compositions_count), value: selected.summary.compositions, active: kind === "composition" && status === "all", select: () => filterSummary("composition", "all", "projects") },
    { label: t(($) => $.running_count), value: selected.summary.running, active: status === "running", select: () => filterSummary("all", "running", "board") },
    { label: t(($) => $.unhealthy_count), value: selected.summary.unhealthy, active: status === "attention", select: () => filterSummary("all", "attention", "board") },
    { label: t(($) => $.offline_count), value: selected.summary.offline, active: status === "offline", select: () => filterSummary("all", "offline", "board") },
    { label: t(($) => $.runtimes_count), value: selected.summary.runtimes, active: view === "runtimes" && kind === "all" && status === "all", select: () => filterSummary("all", "all", "runtimes") },
  ] : [];
  const viewOptions = [{ value: "projects", label: t(($) => $.projects), icon: FolderKanban }, { value: "runtimes", label: t(($) => $.runtimes), icon: Server }, { value: "board", label: t(($) => $.board), icon: Activity }] as const;
  const statusLabels = { all: t(($) => $.all_statuses), attention: t(($) => $.unhealthy_count), starting: t(($) => $.status.starting), running: t(($) => $.status.running), stopping: t(($) => $.status.stopping), stopped: t(($) => $.status.stopped), failed: t(($) => $.status.failed), unhealthy: t(($) => $.status.unhealthy), offline: t(($) => $.status.offline), unknown: t(($) => $.status.unknown) };
  const emptyWorkspace = !!board && board.applications.length === 0;
  const noProjects = !projects.isPending && !projects.isError && projects.data?.length === 0;
  const refresh = () => { void boardQuery.refetch(); void projects.refetch(); void runtimes.refetch(); };
  const dependencyError = projects.error ?? runtimes.error;
  const pending = boardQuery.isPending || projects.isPending;
  const noResults = !selected?.applications.length || view !== "projects" && !selected.instances.length;

  return <div className={cn("flex min-h-0 flex-1 flex-col", embedded && "rounded-xl border")}>
    <CollectionPageHeader icon={Server} title={t(($) => $.title)} actions={<>
      <ApplicationOperations workspaceId={workspaceId} />
      <IconButton label={t(($) => $.refresh)} disabled={boardQuery.isFetching} onClick={refresh}><RefreshCw className={cn("size-4", boardQuery.isFetching && "animate-spin")} /></IconButton>
      {noProjects ? <CollectionCreateButton label={t(($) => $.create_project)} onClick={() => useModalStore.getState().open("create-project")} /> : createApplication(<CollectionCreateButton label={t(($) => $.new)} />)}
    </>} />
    <div data-application-scroll className={cn("min-h-0 flex-1 overflow-y-auto pb-8", PAGE_GUTTER)}>
      {!embedded && !emptyWorkspace && !!board && <div className={cn("grid grid-cols-2 gap-3 py-5 sm:grid-cols-3 xl:grid-cols-6", PAGE_RAIL)}>{summaries.map((summary) => <button key={summary.label} type="button" aria-pressed={summary.active} onClick={summary.select} className={cn("min-w-0 rounded-lg border p-3 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", summary.active && "bg-surface-selected hover:bg-surface-selected")}><span className="flex items-start justify-between gap-2 text-caption text-muted-foreground"><span className="break-words">{summary.label}</span>{summary.active && <Check aria-hidden className="size-3.5 shrink-0 text-foreground" />}</span><span className="mt-2 block text-title tabular-nums">{summary.value}</span></button>)}</div>}
      {!emptyWorkspace && <div className={cn("flex flex-wrap items-end justify-between gap-4 py-3", PAGE_RAIL)}>
        <div role="group" aria-label={t(($) => $.title)} className="flex flex-wrap rounded-lg bg-muted p-1">{viewOptions.map(({ value, label, icon: Icon }) => <Button key={value} size="sm" variant="ghost" aria-pressed={view === value} onClick={() => setView(value)} className={cn("gap-1.5", view === value && "bg-background text-foreground shadow-xs hover:bg-background")}><Icon className="size-3.5" />{label}</Button>)}</div>
        <div className="flex min-w-0 flex-1 basis-full flex-wrap items-end gap-3 lg:basis-0">
          {!presetProjectId && <label className="min-w-36 flex-1 space-y-1 text-caption">{t(($) => $.project)}<ApplicationSelect value={projectId} onChange={setProject} label={t(($) => $.project)} items={[{ value: "all", label: t(($) => $.all_projects) }, ...(projects.data ?? []).map((project) => ({ value: project.id, label: project.title }))]} /></label>}
          {!presetRuntimeId && !presetRuntimeIds && <label className="min-w-36 flex-1 space-y-1 text-caption">{t(($) => $.runtime)}<ApplicationSelect value={runtimeParam} onChange={setRuntime} label={t(($) => $.runtime)} items={[{ value: "all", label: t(($) => $.all_runtimes) }, ...(runtimes.data ?? []).map((runtime) => ({ value: runtime.id, label: runtimeDisplayLabel(runtime) }))]} /></label>}
          <label className="min-w-32 flex-1 space-y-1 text-caption">{t(($) => $.kind)}<ApplicationSelect value={kind} onChange={setKind} label={t(($) => $.kind)} items={[{ value: "all", label: t(($) => $.all_kinds) }, { value: "service", label: t(($) => $.service) }, { value: "composition", label: t(($) => $.composition) }]} /></label>
          <label className="min-w-32 flex-1 space-y-1 text-caption">{t(($) => $.status_filter)}<ApplicationSelect value={status} onChange={setStatus} label={t(($) => $.status_filter)} items={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} /></label>
          <label className="min-w-32 flex-1 space-y-1 text-caption">{t(($) => $.publication_filter)}<ApplicationSelect value={publication} onChange={setPublication} label={t(($) => $.publication_filter)} items={[{ value: "all", label: t(($) => $.all_publications) }, { value: "published", label: t(($) => $.published) }, { value: "unpublished", label: t(($) => $.unpublished) }]} /></label>
          <label className="min-w-40 flex-1 space-y-1 text-caption">{t(($) => $.search)}<span className="relative block"><Search aria-hidden className="pointer-events-none absolute left-2 top-2 size-4 text-muted-foreground" /><Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t(($) => $.search)} className="pl-8" /></span></label>
          {(hasFilters || search) && !noResults && <Button variant="ghost" size="sm" onClick={reset}>{t(($) => $.clear_filters)}</Button>}
        </div>
      </div>}
      {dependencyError && <div role="alert" className={cn("flex flex-wrap items-center gap-3 py-3 text-body text-destructive", PAGE_RAIL)}><p>{dependencyError.message}</p><Button variant="outline" size="sm" onClick={refresh}>{t(($) => $.retry)}</Button></div>}
      {pending ? <div role="status" className="flex justify-center gap-2 py-20 text-body text-muted-foreground"><Loader2 className="size-5 animate-spin" />{t(($) => $.status.checking)}</div> : boardQuery.isError ? <CollectionPageState icon={Server} title={t(($) => $.load_failed)} description={boardQuery.error.message} role="alert" actions={<Button variant="outline" onClick={refresh}>{t(($) => $.retry)}</Button>} /> : emptyWorkspace ? <CollectionPageState icon={Server} title={t(($) => $.empty)} description={noProjects ? t(($) => $.project_required) : t(($) => $.empty_help)} actions={noProjects ? <Button onClick={() => useModalStore.getState().open("create-project")}><Plus />{t(($) => $.create_project)}</Button> : createApplication(<Button><Plus />{t(($) => $.new)}</Button>)} /> : noResults ? <CollectionNoMatches title={view !== "projects" && selected?.applications.length ? t(($) => $.no_instances) : t(($) => $.no_matches)} hasFilters={hasFilters} hasSearch={!!search} onReset={reset}>
        {view !== "projects" && <Button variant="outline" size="sm" onClick={() => setView("projects")}>{t(($) => $.projects)}</Button>}
      </CollectionNoMatches> : board && selected && <div className={cn("space-y-6 py-3", PAGE_RAIL)}><ApplicationResults view={view} board={board} applications={selected.applications} instances={selected.instances} scopedInstances={selected.scopedInstances} projects={projects.data ?? []} runtimes={runtimes.data ?? []} selectedRuntimeIds={runtimeIds} showAll={!!search || kind !== "all" || status !== "all" || publication !== "all"} selectedInstanceIds={selectedInstanceIds} onToggleInstance={toggleInstance} />{Object.keys(selectedInstances).length > 0 && <ApplicationBulkActions workspaceId={workspaceId} items={Object.values(selectedInstances)} onClear={() => setSelectedInstances({})} />}</div>}
    </div>
  </div>;
}
