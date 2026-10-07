"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, FolderKanban, LayoutGrid, Loader2, Plus, RefreshCw, Search, Server } from "lucide-react";
import { applicationBoardOptions, applicationServiceIds, applicationStatus, topLevelApplications, type Application, type ApplicationBoard } from "@multica/core/applications";
import { projectListOptions } from "@multica/core/projects";
import { runtimeDisplayLabel, runtimeListOptions } from "@multica/core/runtimes";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { ApplicationEditor } from "./application-editor";
import { ApplicationSelect, ApplicationStatusBadge } from "./controls";

function ApplicationCard({ application, board }: { application: Application; board: ApplicationBoard }) {
  const { t } = useT("applications");
  const paths = useWorkspacePaths();
  const ids = applicationServiceIds(application, board.applications);
  const instances = board.instances.filter((instance) => ids.has(instance.application_id));
  const status = applicationStatus(application, board.applications, board.instances);
  const members = application.relations.filter((relation) => relation.type === "contains");
  const byId = new Map(board.applications.map((app) => [app.id, app]));
  return <div className="min-w-0 rounded-xl border bg-card p-4">
    <div className="flex items-start justify-between gap-3">
      <AppLink href={paths.applicationDetail(application.id)} className="flex min-w-0 items-center gap-2.5 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">{application.kind === "composition" ? <LayoutGrid className="size-4" /> : <Server className="size-4" />}</span>
        <div className="min-w-0"><p className="break-words text-body font-medium">{application.name}</p><p className="mt-0.5 text-caption text-muted-foreground">{application.kind === "composition" ? t(($) => $.composition) : application.kind === "service" ? t(($) => $.service) : t(($) => $.unknown)}</p></div>
      </AppLink>
      <ApplicationStatusBadge status={status} />
    </div>
    <div className="mt-4 flex flex-wrap items-center gap-3 text-caption text-muted-foreground"><span>{t(($) => $.instance_count, { count: instances.length })}</span>{members.length > 0 && <span>{t(($) => $.member_count, { count: members.length })}</span>}</div>
    {members.length > 0 && <div className="mt-3 flex flex-wrap gap-1.5">{members.map((relation) => {
      const member = byId.get(relation.target_id);
      return member ? <AppLink key={member.id} href={paths.applicationDetail(member.id)} className="max-w-full truncate rounded-md bg-muted px-2 py-1 text-caption text-muted-foreground hover:text-foreground">{member.name}</AppLink> : null;
    })}</div>}
  </div>;
}

export function ApplicationsPage({ projectId: presetProjectId, runtimeId: presetRuntimeId, runtimeIds: presetRuntimeIds, embedded = false }: { projectId?: string; runtimeId?: string; runtimeIds?: string[]; embedded?: boolean }) {
  const { t } = useT("applications");
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const boardQuery = useQuery(applicationBoardOptions(workspaceId));
  const projects = useQuery(projectListOptions(workspaceId));
  const runtimes = useQuery(runtimeListOptions(workspaceId));
  const [view, setView] = useState<"projects" | "runtimes" | "board">(presetRuntimeId || presetRuntimeIds ? "runtimes" : "projects");
  const [search, setSearch] = useState("");
  const [projectFilter, setProjectFilter] = useState(presetProjectId ?? "all");
  const board = boardQuery.data;
  const applications = useMemo(() => (board?.applications ?? []).filter((application) => (projectFilter === "all" || application.project_id === projectFilter) && (!search.trim() || application.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()))), [board?.applications, projectFilter, search]);
  const filteredIds = new Set(applications.map((app) => app.id));
  const selectedRuntimeIds = presetRuntimeIds ?? (presetRuntimeId ? [presetRuntimeId] : null);
  const instances = (board?.instances ?? []).filter((instance) => filteredIds.has(instance.application_id) && (!selectedRuntimeIds || selectedRuntimeIds.includes(instance.runtime_id)));
  const runtimeById = new Map((runtimes.data ?? []).map((runtime) => [runtime.id, runtime]));
  const summaryCards = board ? [
    { label: t(($) => $.services_count), value: board.summary.services }, { label: t(($) => $.compositions_count), value: board.summary.compositions },
    { label: t(($) => $.running_count), value: board.summary.running }, { label: t(($) => $.unhealthy_count), value: board.summary.unhealthy },
    { label: t(($) => $.offline_count), value: board.summary.offline }, { label: t(($) => $.runtimes_count), value: board.summary.runtimes },
  ] : [];
  const viewOptions = [{ value: "projects", label: t(($) => $.projects), icon: FolderKanban }, { value: "runtimes", label: t(($) => $.runtimes), icon: Server }, { value: "board", label: t(($) => $.board), icon: Activity }] as const;

  return <div className={cn("flex min-h-0 flex-1 flex-col", embedded && "rounded-xl border")}>
    <CollectionPageHeader icon={Server} title={t(($) => $.title)} count={board?.summary.services} actions={<>
      <Button variant="ghost" size="icon-sm" aria-label={t(($) => $.refresh)} disabled={boardQuery.isFetching} onClick={() => void boardQuery.refetch()}><RefreshCw className={cn("size-4", boardQuery.isFetching && "animate-spin")} /></Button>
      <ApplicationEditor workspaceId={workspaceId} projectId={presetProjectId} trigger={<Button size="sm"><Plus />{t(($) => $.new)}</Button>} />
    </>} />
    <div className={cn("min-h-0 flex-1 overflow-y-auto pb-8", PAGE_GUTTER)}>
      {!embedded && <div className={cn("grid grid-cols-2 gap-3 py-5 sm:grid-cols-3 xl:grid-cols-6", PAGE_RAIL)}>{summaryCards.map((card) => <div key={card.label} className="rounded-lg border bg-card p-3"><p className="truncate text-caption text-muted-foreground">{card.label}</p><p className="mt-2 font-mono text-title tabular-nums">{card.value}</p></div>)}</div>}
      <div className={cn("flex flex-wrap items-center justify-between gap-3 py-3", PAGE_RAIL)}>
        <div role="group" aria-label={t(($) => $.title)} className="flex rounded-lg bg-muted p-1">{viewOptions.map(({ value, label, icon: Icon }) => <Button key={value} size="sm" variant="ghost" aria-pressed={view === value} onClick={() => setView(value)} className={cn("gap-1.5", view === value && "bg-background text-foreground shadow-xs hover:bg-background")}><Icon className="size-3.5" /><span className="hidden sm:inline">{label}</span><span className="sr-only sm:hidden">{label}</span></Button>)}</div>
        <div className="flex min-w-0 flex-1 flex-wrap justify-end gap-2 sm:flex-none">
          {!presetProjectId && <div className="min-w-36"><ApplicationSelect value={projectFilter} onChange={setProjectFilter} label={t(($) => $.project)} items={[{ value: "all", label: t(($) => $.all_projects) }, ...(projects.data ?? []).map((project) => ({ value: project.id, label: project.title }))]} /></div>}
          <div className="relative min-w-40 flex-1 sm:w-56"><Search aria-hidden className="pointer-events-none absolute left-2 top-2 size-4 text-muted-foreground" /><Input value={search} onChange={(event) => setSearch(event.target.value)} aria-label={t(($) => $.search)} placeholder={t(($) => $.search)} className="pl-8" /></div>
        </div>
      </div>
      {boardQuery.isPending ? <div role="status" className="flex justify-center py-20"><Loader2 className="size-6 animate-spin text-muted-foreground" /><span className="sr-only">{t(($) => $.status.checking)}</span></div> : boardQuery.isError ? <CollectionPageState icon={Server} title={t(($) => $.load_failed)} description={boardQuery.error.message} role="alert" actions={<Button variant="outline" onClick={() => void boardQuery.refetch()}>{t(($) => $.retry)}</Button>} /> : !board || !applications.length ? <CollectionPageState icon={Server} title={search || projectFilter !== "all" ? t(($) => $.no_matches) : t(($) => $.empty)} description={t(($) => $.empty_help)} /> : <div className={cn("space-y-6 py-3", PAGE_RAIL)}>
        {view === "projects" && (projects.data ?? []).filter((project) => applications.some((app) => app.project_id === project.id)).map((project) => {
          const projectApps = applications.filter((app) => app.project_id === project.id);
          const roots = search ? projectApps : topLevelApplications(projectApps);
          return <section key={project.id} className="space-y-3"><div className="flex items-center gap-2"><FolderKanban className="size-4 text-muted-foreground" /><AppLink href={paths.projectDetail(project.id)} className="min-w-0 break-words text-body font-medium hover:underline">{project.title}</AppLink><span className="text-caption text-muted-foreground">{projectApps.length}</span></div><div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">{roots.map((app) => <ApplicationCard key={app.id} application={app} board={board} />)}</div></section>;
        })}
        {view === "runtimes" && [...new Set([...instances.map((instance) => instance.runtime_id), ...(selectedRuntimeIds ?? [])])].map((runtimeId) => {
          const runtime = runtimeById.get(runtimeId);
          const runtimeInstances = instances.filter((instance) => instance.runtime_id === runtimeId);
          return <section key={runtimeId} className="space-y-3 rounded-xl border p-4"><div className="flex flex-wrap items-center justify-between gap-2"><h2 className="flex min-w-0 items-center gap-2 text-body font-medium"><Server className="size-4 shrink-0 text-muted-foreground" /><span className="break-words">{runtime ? runtimeDisplayLabel(runtime) : t(($) => $.private_runtime)}</span></h2><span className="text-caption text-muted-foreground">{t(($) => $.instance_count, { count: runtimeInstances.length })}</span></div>
            {runtimeInstances.length ? <div className="divide-y">{runtimeInstances.map((instance) => {
              const app = board.applications.find((entry) => entry.id === instance.application_id);
              return app ? <div key={instance.id} className="flex flex-wrap items-center justify-between gap-3 py-3"><AppLink href={paths.applicationDetail(app.id)} className="min-w-0 break-words text-body hover:underline">{app.name}</AppLink><div className="flex items-center gap-3"><span className="text-caption text-muted-foreground">{t(($) => $.revision)} {instance.observed_revision || instance.revision}</span><ApplicationStatusBadge status={instance.status} /></div></div> : null;
            })}</div> : <p className="py-4 text-caption text-muted-foreground">{t(($) => $.no_runtime_instances)}</p>}
          </section>;
        })}
        {view === "board" && <div className="grid gap-3 lg:grid-cols-4">{(["starting", "running", "failed", "offline", "stopping", "stopped", "unknown"] as const).map((column) => {
          const rows = instances.filter((instance) => column === "failed" ? instance.status === "failed" || instance.status === "unhealthy" : instance.status === column);
          return <section key={column} className="min-w-0 rounded-xl bg-muted/40 p-3"><div className="mb-3 flex items-center justify-between"><ApplicationStatusBadge status={column} /><span className="text-caption tabular-nums text-muted-foreground">{rows.length}</span></div><div className="space-y-2">{rows.map((instance) => {
            const app = board.applications.find((entry) => entry.id === instance.application_id);
            const runtime = runtimeById.get(instance.runtime_id);
            return app ? <AppLink key={instance.id} href={paths.applicationDetail(app.id)} className="block rounded-lg border bg-card p-3 hover:border-foreground/25"><p className="break-words text-body font-medium">{app.name}</p><p className="mt-1 break-words text-caption text-muted-foreground">{runtime ? runtimeDisplayLabel(runtime) : t(($) => $.private_runtime)}</p>{instance.error && <p className="mt-2 break-words text-caption text-destructive">{instance.error}</p>}</AppLink> : null;
          })}</div></section>;
        })}</div>}
      </div>}
    </div>
  </div>;
}
