"use client";

import { useCallback, useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { projectSupervisionOptions, projectSupervisionKey } from "@multica/core/projects";
import { issueStatusListOptions } from "@multica/core/issue-statuses";
import type { Project, ProjectSupervisionConfig } from "@multica/core/types/project";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink, useNavigationGuard } from "../../navigation";
import { useT } from "../../i18n";
import { useStatusLabel } from "../../issues/utils/status-label";

type Category="ready"|"unassigned"|"executing"|"review"|"blocked"|"paused"|"stalled";
const categories:Category[]=["ready","unassigned","executing","review","blocked","paused","stalled"];
const numericFields=["max_in_flight","batch_size","scan_interval_seconds","stale_after_seconds","no_progress_limit"] as const;

export function ProjectSupervisionPanel({project,canManage}:{project:Project;canManage:boolean}){
 const {t}=useT("projects");const wsId=useWorkspaceId();const paths=useWorkspacePaths();const qc=useQueryClient();
 const query=useQuery(projectSupervisionOptions(wsId,project.id));
 const statuses=useQuery(issueStatusListOptions(wsId));
 const statusLabel=useStatusLabel(wsId);
 const [draft,setDraft]=useState<{enabled:boolean;config:ProjectSupervisionConfig;revision:number}|null>(null);
 const [filter,setFilter]=useState<Category|null>(null);
 const guard=useCallback(()=>!draft||window.confirm(t($=>$.detail.scope_unsaved)),[draft,t]);useNavigationGuard(guard);
 useEffect(()=>{if(!draft)return;const prevent=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue=""};window.addEventListener("beforeunload",prevent);return()=>window.removeEventListener("beforeunload",prevent)},[draft]);
 const key=projectSupervisionKey(wsId,project.id);
 const save=useMutation({mutationFn:(input:NonNullable<typeof draft>)=>api.saveProjectSupervision(project.id,input),onSuccess:view=>{qc.setQueryData(key,view);setDraft(null)}});
 const check=useMutation({mutationFn:()=>api.checkProjectSupervision(project.id),onSuccess:view=>qc.setQueryData(key,view)});
 const view=query.data;
 if(!view)return <section className="m-3 rounded-lg border p-3 text-caption" aria-label={t($=>$.supervision.title)}>{query.isPending?<p role="status">{t($=>$.supervision.loading)}</p>:<div role="alert">{t($=>$.supervision.unavailable)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.supervision.retry)}</Button></div>}</section>;
 const value=draft??{enabled:view.enabled,config:view.config,revision:view.revision};
 const update=(patch:Partial<typeof value>)=>setDraft({...value,...patch});
 const config=(patch:Partial<ProjectSupervisionConfig>)=>update({config:{...value.config,...patch}});
 let reason=view.last_reason;
 if(view.last_task_status&&!['needs_human','runtime_offline','runtime_upgrade_required','permission_denied','authority_revoked','no_agent_lead','project_ended'].includes(reason)){
  if(["queued","deferred"].includes(view.last_task_status))reason="coordination_queued";
  else if(["running","dispatched","waiting_local_directory"].includes(view.last_task_status))reason="coordination_active";
  else if(reason==="coordination_active"||reason==="coordination_queued")reason="waiting";
 }
 const reasonLabels:Record<string,string>={coordination_active:t($=>$.supervision.active),coordination_queued:t($=>$.supervision.queued),no_actionable_work:t($=>$.supervision.healthy),work_released:t($=>$.supervision.released),capacity_wait:t($=>$.supervision.capacity),agent_capacity:t($=>$.supervision.capacity),lead_capacity:t($=>$.supervision.capacity),runtime_offline:t($=>$.supervision.offline),runtime_upgrade_required:t($=>$.supervision.upgrade),no_agent_lead:t($=>$.supervision.no_lead),needs_human:t($=>$.supervision.needs_human),no_verified_progress:t($=>$.supervision.no_progress),missing_report:t($=>$.supervision.no_progress),project_ended:t($=>$.supervision.project_paused),authority_revoked:t($=>$.supervision.permission),permission_denied:t($=>$.supervision.permission)};
 const labels:Record<Category,string>={ready:t($=>$.supervision.ready),unassigned:t($=>$.supervision.unassigned),executing:t($=>$.supervision.executing),review:t($=>$.supervision.review),blocked:t($=>$.supervision.blocked),paused:t($=>$.supervision.paused),stalled:t($=>$.supervision.stalled)};
 return <section className="m-3 max-h-[50vh] shrink-0 space-y-3 overflow-y-auto rounded-lg border p-3 text-caption" aria-label={t($=>$.supervision.title)}>
  <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-medium">{t($=>$.supervision.title)}</h2><span>{view.enabled?(reasonLabels[reason]??t($=>$.supervision.waiting)):t($=>$.supervision.disabled)}</span>{canManage&&<Button size="sm" variant="outline" disabled={!view.enabled||check.isPending} aria-busy={check.isPending} onClick={()=>check.mutate()}>{t($=>$.supervision.check)}</Button>}</div>
  <div className="flex flex-wrap gap-2">{categories.map(category=><button key={category} type="button" aria-pressed={filter===category} className={`rounded-md border px-2 py-1 ${filter===category?"bg-accent font-medium":"hover:bg-accent/50"}`} onClick={()=>setFilter(filter===category?null:category)}>{labels[category]} {view.snapshot.counts[category]}</button>)}</div>
  {view.last_result.summary&&<p className="break-words">{view.last_result.summary}</p>}
  {view.last_result.wait_reason&&<p className="break-words text-muted-foreground">{view.last_result.wait_reason}</p>}
  <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">{view.last_checked_at&&<span>{t($=>$.supervision.last_check)} {new Date(view.last_checked_at).toLocaleString()}</span>}{view.enabled&&view.next_check_at&&<span>{t($=>$.supervision.next_check)} {new Date(view.next_check_at).toLocaleString()}</span>}{view.dirty_version>view.handled_version&&<span>{t($=>$.supervision.pending)}</span>}</div>
  {check.isError&&<p role="alert">{t($=>$.supervision.check_failed)}</p>}
  {filter&&<div className="max-h-48 space-y-1 overflow-y-auto">{view.snapshot.issues.filter(issue=>issue.category===filter).map(issue=><AppLink key={issue.id} href={paths.issueDetail(issue.identifier||issue.id)} className="block truncate rounded-sm px-2 py-1 hover:bg-accent">{issue.identifier} · {issue.title}</AppLink>)}</div>}
  {canManage&&<details><summary className="cursor-pointer font-medium">{t($=>$.supervision.configure)}</summary><div className="mt-3 space-y-3">
   <label className="flex items-center gap-2"><input type="checkbox" checked={value.enabled} disabled={save.isPending} onChange={event=>update({enabled:event.target.checked})}/>{t($=>$.supervision.enable)}</label>
   {project.lead_type!=="agent"&&<p>{t($=>$.supervision.no_lead)}</p>}
   <label className="flex items-center gap-2"><input type="checkbox" checked={value.config.auto_advance} disabled={save.isPending} onChange={event=>config({auto_advance:event.target.checked})}/>{t($=>$.supervision.auto_advance)}</label>
   <p className="text-muted-foreground">{t($=>$.supervision.auto_help)}</p>
   <fieldset disabled={save.isPending} className="grid gap-3 sm:grid-cols-2">{numericFields.map(field=><label key={field} className="flex items-center justify-between gap-2">{t($=>$.supervision.fields[field])}<input type="number" className="w-24 rounded-md border bg-background px-2 py-1" min={field==="scan_interval_seconds"||field==="stale_after_seconds"?60:1} value={value.config[field]} onChange={event=>config({[field]:Number(event.target.value)})}/></label>)}</fieldset>
   <fieldset disabled={save.isPending} className="flex flex-wrap gap-3"><legend className="mb-2 font-medium">{t($=>$.supervision.ready_statuses)}</legend>{statuses.data?.filter(status=>status.category==="unstarted"&&status.key!=="backlog"&&status.key!=="triage"&&!status.archived_at).map(status=><label key={status.key} className="flex items-center gap-2"><input type="checkbox" checked={value.config.ready_statuses.includes(status.key)} onChange={event=>config({ready_statuses:event.target.checked?[...value.config.ready_statuses,status.key]:value.config.ready_statuses.filter(key=>key!==status.key)})}/>{statusLabel(status.key)}</label>)}</fieldset>
   {save.isError&&<p role="alert">{t($=>$.supervision.save_failed)}</p>}
   <div className="flex gap-2"><Button size="sm" disabled={!draft||save.isPending} aria-busy={save.isPending} onClick={()=>{if(draft)save.mutate(draft)}}>{t($=>$.supervision.save)}</Button>{draft&&<Button size="sm" variant="outline" disabled={save.isPending} onClick={()=>{setDraft(null);save.reset()}}>{t($=>$.supervision.cancel)}</Button>}</div>
  </div></details>}
 </section>;
}
