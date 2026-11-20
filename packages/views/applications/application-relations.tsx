"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Loader2, Plus, Trash2 } from "@multica/ui/icons";
import { toast } from "sonner";
import { applicationPlanOptions, useUpdateApplication, type Application, type ApplicationBoard, type ApplicationRelation } from "@multica/core/applications";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { ApplicationSelect } from "./controls";

export function ApplicationRelations({ workspaceId, application, board }: { workspaceId: string; application: Application; board: ApplicationBoard }) {
  const { t } = useT("applications");
  const paths = useWorkspacePaths();
  const [relations, setRelations] = useState(application.relations);
  const [revision, setRevision] = useState(application.revision);
  const [error, setError] = useState("");
  const update = useUpdateApplication(workspaceId, application.id);
  const plan = useQuery(applicationPlanOptions(workspaceId, application.id, application.revision));
  const candidates = board.applications.filter((app) => app.project_id === application.project_id && app.id !== application.id);
  const byId = new Map(board.applications.map((app) => [app.id, app]));
  const change = (index: number, patch: Partial<ApplicationRelation>) => setRelations((current) => current.map((relation, row) => row === index ? { ...relation, ...patch } : relation));
  const save = async () => {
    try { setError(""); const saved = await update.mutateAsync({ revision, relations }); setRevision(saved.revision); toast.success(t(($) => $.saved)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t(($) => $.invalid_config)); }
  };
  const relationItems = [{ value: "depends_on", label: t(($) => $.depends_on) }, { value: "related", label: t(($) => $.related) }, ...(application.kind === "composition" ? [{ value: "contains", label: t(($) => $.contains) }] : [])];

  return <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(240px,0.65fr)]">
    <section className="space-y-4"><div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-body font-medium">{t(($) => $.orchestration)}</h2><Button size="sm" variant="outline" disabled={!candidates.length || update.isPending} onClick={() => setRelations((current) => [...current, { source_id: application.id, target_id: candidates[0]!.id, type: application.kind === "composition" ? "contains" : "depends_on", required: true, condition: application.kind === "composition" ? "" : "healthy", start_external: false }])}><Plus />{t(($) => $.add_relation)}</Button></div>
      {!relations.length && <p className="rounded-lg border p-4 text-caption text-muted-foreground">{t(($) => $.no_relations)}</p>}
      {relations.map((relation, index) => <div key={index} className="space-y-3 rounded-xl border p-4">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2"><ApplicationSelect value={relation.type} onChange={(value) => change(index, { type: value === "contains" ? "contains" : value === "related" ? "related" : "depends_on", condition: value === "depends_on" ? "healthy" : "", start_external: false })} items={relationItems} label={t(($) => $.relation)} disabled={update.isPending} /><ApplicationSelect value={relation.target_id} onChange={(target_id) => change(index, { target_id })} items={candidates.map((app) => ({ value: app.id, label: app.name }))} label={t(($) => $.target)} disabled={update.isPending} /><Button size="icon-sm" variant="ghost" aria-label={t(($) => $.remove)} disabled={update.isPending} onClick={() => setRelations((current) => current.filter((_relation, row) => row !== index))}><Trash2 /></Button></div>
        {relation.type === "contains" && <label className="flex items-center justify-between gap-4 text-caption">{t(($) => $.required)}<Switch checked={relation.required} onCheckedChange={(required) => change(index, { required })} /></label>}
        {relation.type === "depends_on" && <><ApplicationSelect value={relation.condition} onChange={(value) => change(index, { condition: value === "started" ? "started" : "healthy" })} items={[{ value: "healthy", label: t(($) => $.healthy) }, { value: "started", label: t(($) => $.started) }]} label={t(($) => $.condition)} /><label className="flex items-center justify-between gap-4 text-caption">{t(($) => $.start_external)}<Switch checked={relation.start_external} onCheckedChange={(start_external) => change(index, { start_external })} /></label></>}
      </div>)}
      {error && <p role="alert" className="break-words text-caption text-destructive">{error}</p>}
      <Button disabled={update.isPending} aria-busy={update.isPending} onClick={() => void save()}>{update.isPending && <Loader2 className="animate-spin" />}{t(($) => $.save_relations)}</Button>
    </section>
    <section className="h-fit space-y-4 rounded-xl border p-4"><h2 className="text-body font-medium">{t(($) => $.plan)}</h2>{plan.isPending ? <Loader2 className="size-4 animate-spin" /> : plan.isError ? <p className="break-words text-caption text-muted-foreground">{plan.error.message}</p> : <><div className="overflow-x-auto pb-1"><div className="flex min-w-max items-stretch gap-2">{plan.data.waves.map((wave, index) => <div key={index} className="flex items-center gap-2"><div className="w-44 space-y-2"><p className="text-caption text-muted-foreground">{t(($) => $.wave, { number: index + 1 })}</p>{wave.map((id) => <AppLink key={id} href={paths.applicationDetail(id)} className="block break-words rounded-lg bg-muted px-3 py-2 text-body hover:text-primary">{byId.get(id)?.name ?? id}</AppLink>)}</div>{index < plan.data.waves.length - 1 && <ArrowRight aria-hidden className="size-4 shrink-0 text-muted-foreground" />}</div>)}</div></div><div className="space-y-2 border-t pt-4"><h3 className="text-caption font-medium text-muted-foreground">{t(($) => $.dependency_graph)}</h3>{plan.data.nodes.filter((node) => node.dependencies.length > 0).map((node) => <div key={node.id} className="space-y-1 rounded-lg bg-muted/40 p-3"><AppLink href={paths.applicationDetail(node.id)} className="break-words text-body font-medium hover:underline">{byId.get(node.id)?.name ?? node.id}</AppLink>{node.dependencies.map((dependency) => <p key={`${node.id}-${dependency.id}`} className="flex flex-wrap items-center gap-1 text-caption text-muted-foreground"><ArrowRight aria-hidden className="size-3" />{byId.get(dependency.id)?.name ?? dependency.id} · {dependency.condition === "healthy" ? t(($) => $.healthy) : dependency.condition === "started" ? t(($) => $.started) : t(($) => $.unknown)}</p>)}</div>)}</div></>}</section>
  </div>;
}
