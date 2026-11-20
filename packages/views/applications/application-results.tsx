"use client";

import { FolderKanban, LayoutGrid, Server } from "@multica/ui/icons";
import { applicationServiceIds, applicationStatus, topLevelApplications, type Application, type ApplicationBoard, type ApplicationInstance } from "@multica/core/applications";
import { runtimeDisplayLabel } from "@multica/core/runtimes";
import type { AgentRuntime } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { ApplicationStatusBadge } from "./controls";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import type { ApplicationBulkOperationItem } from "@multica/core/applications";
import type { ApplicationView } from "./application-view-state";

function ApplicationCard({ application, board }: { readonly application: Application; readonly board: ApplicationBoard }) {
  const { t } = useT("applications");
  const paths = useWorkspacePaths();
  const ids = applicationServiceIds(application, board.applications);
  const instances = board.instances.filter((instance) => ids.has(instance.application_id));
  const status = applicationStatus(application, board.applications, instances);
  const members = application.relations.filter((relation) => relation.type === "contains");
  const byId = new Map(board.applications.map((app) => [app.id, app]));
  return <article className="min-w-0 rounded-xl border bg-card p-4">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <AppLink href={paths.applicationDetail(application.id)} className="flex min-w-0 items-center gap-2.5 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {application.kind === "composition" ? <LayoutGrid className="size-4 shrink-0 text-muted-foreground" /> : <Server className="size-4 shrink-0 text-muted-foreground" />}
        <div className="min-w-0"><p className="break-words text-body font-medium">{application.name}</p><p className="mt-0.5 text-caption text-muted-foreground">{application.kind === "composition" ? t(($) => $.composition) : application.kind === "service" ? t(($) => $.service) : t(($) => $.unknown)}</p></div>
      </AppLink>
      <ApplicationStatusBadge status={status} />
    </div>
    {application.description && <p className="mt-3 break-words text-pretty text-body text-muted-foreground">{application.description}</p>}
    <div className="mt-4 flex flex-wrap items-center gap-3 text-caption text-muted-foreground"><span>{t(($) => $.instance_count, { count: instances.length })}</span>{members.length > 0 && <span>{t(($) => $.member_count, { count: members.length })}</span>}</div>
    {members.length > 0 && <div className="mt-3 flex flex-wrap gap-1.5">{members.map((relation) => {
      const member = byId.get(relation.target_id);
      return member ? <AppLink key={member.id} href={paths.applicationDetail(member.id)} className="max-w-full truncate rounded-md bg-muted px-2 py-1 text-caption text-muted-foreground hover:text-foreground">{member.name}</AppLink> : null;
    })}</div>}
  </article>;
}

interface ApplicationResultsProps {
  readonly view: ApplicationView;
  readonly board: ApplicationBoard;
  readonly applications: readonly Application[];
  readonly instances: readonly ApplicationInstance[];
  readonly scopedInstances: readonly ApplicationInstance[];
  readonly projects: readonly { readonly id: string; readonly title: string }[];
  readonly runtimes: readonly AgentRuntime[];
  readonly selectedRuntimeIds: readonly string[] | null;
  readonly showAll: boolean;
  readonly selectedInstanceIds: ReadonlySet<string>;
  readonly onToggleInstance: (instanceId: string, instance: ApplicationBulkOperationItem) => void;
}

export function ApplicationResults({ view, board, applications, instances, scopedInstances, projects, runtimes, selectedRuntimeIds, showAll, selectedInstanceIds, onToggleInstance }: ApplicationResultsProps) {
  const { t } = useT("applications");
  const paths = useWorkspacePaths();
  const runtimeById = new Map(runtimes.map((runtime) => [runtime.id, runtime]));
  const scopedBoard = { ...board, instances: [...scopedInstances] };
  if (view === "projects") return <>{projects.filter((project) => applications.some((app) => app.project_id === project.id)).map((project) => {
    const projectApps = applications.filter((app) => app.project_id === project.id);
    const roots = showAll ? projectApps : topLevelApplications(projectApps);
    return <section key={project.id} className="space-y-3">
      <div className="flex items-center gap-2"><FolderKanban className="size-4 text-muted-foreground" /><AppLink href={paths.projectDetail(project.id)} className="min-w-0 break-words text-body font-medium hover:underline">{project.title}</AppLink><span className="text-caption tabular-nums text-muted-foreground">{projectApps.length}</span></div>
      <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">{roots.map((application) => <ApplicationCard key={application.id} application={application} board={scopedBoard} />)}</div>
    </section>;
  })}</>;
  if (view === "runtimes") return <>{[...new Set([...instances.map((instance) => instance.runtime_id), ...(selectedRuntimeIds ?? [])])].map((runtimeId) => {
    const runtime = runtimeById.get(runtimeId);
    const runtimeInstances = instances.filter((instance) => instance.runtime_id === runtimeId);
    return <section key={runtimeId} className="space-y-3 rounded-xl border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="flex min-w-0 items-center gap-2 text-body font-medium"><Server className="size-4 shrink-0 text-muted-foreground" /><span className="break-words">{runtime ? runtimeDisplayLabel(runtime) : t(($) => $.private_runtime)}</span></h2><span className="text-caption text-muted-foreground">{t(($) => $.instance_count, { count: runtimeInstances.length })}</span></div>
      {runtimeInstances.length ? <div className="divide-y">{runtimeInstances.map((instance) => {
        const app = board.applications.find((entry) => entry.id === instance.application_id);
              return app ? <div key={instance.id} className="flex flex-wrap items-center justify-between gap-3 py-3"><div className="flex min-w-0 items-center gap-2"><Checkbox checked={selectedInstanceIds.has(instance.id)} onCheckedChange={() => onToggleInstance(instance.id, { applicationId: app.id, revision: app.revision, runtimeId: instance.runtime_id })} aria-label={t(($) => $.select_instance, { name: app.name })} /><AppLink href={paths.applicationDetail(app.id)} className="min-w-0 break-words text-body hover:underline">{app.name}</AppLink></div><div className="flex items-center gap-3"><span className="text-caption text-muted-foreground">{t(($) => $.revision)} {instance.observed_revision || instance.revision}</span><ApplicationStatusBadge status={instance.status} /></div></div> : null;
      })}</div> : <p className="py-4 text-caption text-muted-foreground">{t(($) => $.no_runtime_instances)}</p>}
    </section>;
  })}</>;
  return <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{(["starting", "running", "failed", "unhealthy", "offline", "stopping", "stopped", "unknown"] as const).map((column) => {
    const rows = instances.filter((instance) => instance.status === column);
    return <section key={column} className="min-w-0 rounded-xl bg-muted/40 p-3">
      <div className="mb-3 flex items-center justify-between"><ApplicationStatusBadge status={column} /><span className="text-caption tabular-nums text-muted-foreground">{rows.length}</span></div>
      <div className="space-y-2">{rows.map((instance) => {
        const app = board.applications.find((entry) => entry.id === instance.application_id);
        const runtime = runtimeById.get(instance.runtime_id);
            return app ? <div key={instance.id} className="flex gap-2 rounded-lg border bg-card p-3 hover:border-foreground/25"><Checkbox checked={selectedInstanceIds.has(instance.id)} onCheckedChange={() => onToggleInstance(instance.id, { applicationId: app.id, revision: app.revision, runtimeId: instance.runtime_id })} aria-label={t(($) => $.select_instance, { name: app.name })} /><AppLink href={paths.applicationDetail(app.id)} className="min-w-0 flex-1"><p className="break-words text-body font-medium">{app.name}</p><p className="mt-1 break-words text-caption text-muted-foreground">{runtime ? runtimeDisplayLabel(runtime) : t(($) => $.private_runtime)}</p>{instance.error && <p className="mt-2 break-words text-caption text-destructive">{instance.error}</p>}</AppLink></div> : null;
      })}</div>
    </section>;
  })}</div>;
}
