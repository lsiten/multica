"use client";
import { issueRouteRef } from "@multica/core/paths/paths";

import { humanRequestsOptions, useHumanRequestRealtime } from "@multica/core/human-requests";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { projectSupervisionOptions, projectSupervisionKey } from "@multica/core/projects";
import type { Project } from "@multica/core/types/project";
import { Button } from "@multica/ui/components/ui/button";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { AppLink } from "../../navigation";
import { useLocale, useT } from "../../i18n";
import { ProjectSupervisionSettings } from "./project-supervision-settings";

type Category="ready"|"unassigned"|"executing"|"review"|"blocked"|"paused"|"stalled";
const categories:Category[]=["ready","unassigned","executing","review","blocked","paused","stalled"];

export function ProjectSupervisionPanel({project,canManage}:{project:Project;canManage:boolean}){
 const {t}=useT("projects");const locale=useLocale();const wsId=useWorkspaceId();const paths=useWorkspacePaths();const qc=useQueryClient();
 const query=useQuery(projectSupervisionOptions(wsId,project.id));
 const [filter,setFilter]=useState<Category|null>(null);
 const key=projectSupervisionKey(wsId,project.id);
 const check=useMutation({mutationFn:()=>api.checkProjectSupervision(project.id),onSuccess:view=>qc.setQueryData(key,view)});
 const requests=useQuery(humanRequestsOptions(wsId,{project_id:project.id}));
 useHumanRequestRealtime(wsId);
 const hasPendingRequests=requests.data?.some(request=>request.status==="pending")===true;
 const view=query.data;
 if(!view)return <section className="rounded-lg border p-3 text-caption" aria-label={t($=>$.supervision.title)}>{query.isPending?<p role="status">{t($=>$.supervision.loading)}</p>:<div role="alert">{t($=>$.supervision.unavailable)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.supervision.retry)}</Button></div>}</section>;
 let reason=view.last_reason;
 if(view.last_task_status&&!['needs_human','runtime_offline','runtime_upgrade_required','permission_denied','authority_revoked','no_agent_lead','project_ended'].includes(reason)){
  if(["queued","deferred"].includes(view.last_task_status))reason="coordination_queued";
  else if(["running","dispatched","waiting_local_directory"].includes(view.last_task_status))reason="coordination_active";
  else if(reason==="coordination_active"||reason==="coordination_queued")reason="waiting";
 }
 const reasonLabels:Record<string,string>={coordination_active:t($=>$.supervision.active),coordination_queued:t($=>$.supervision.queued),no_actionable_work:t($=>$.supervision.healthy),work_released:t($=>$.supervision.released),capacity_wait:t($=>$.supervision.capacity),agent_capacity:t($=>$.supervision.capacity),lead_capacity:t($=>$.supervision.capacity),runtime_offline:t($=>$.supervision.offline),runtime_upgrade_required:t($=>$.supervision.upgrade),no_agent_lead:t($=>$.supervision.no_lead),needs_human:t($=>$.supervision.needs_human),no_verified_progress:t($=>$.supervision.no_progress),missing_report:t($=>$.supervision.no_progress),project_ended:t($=>$.supervision.project_paused),authority_revoked:t($=>$.supervision.permission),permission_denied:t($=>$.supervision.permission)};
 const labels:Record<Category,string>={ready:t($=>$.supervision.ready),unassigned:t($=>$.supervision.unassigned),executing:t($=>$.supervision.executing),review:t($=>$.supervision.review),blocked:t($=>$.supervision.blocked),paused:t($=>$.supervision.paused),stalled:t($=>$.supervision.stalled)};
 return <section className="space-y-3 rounded-lg border p-3 text-caption" aria-label={t($=>$.supervision.title)}>
  <div className="flex flex-wrap items-center justify-between gap-3">
   <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1"><h2 className="font-medium">{t($=>$.supervision.title)}</h2><span role="status" className="text-muted-foreground">{view.enabled?(reasonLabels[reason]??t($=>$.supervision.waiting)):t($=>$.supervision.disabled)}</span></div>
   {canManage&&<div className="flex flex-wrap items-center gap-2">{view.enabled&&<Button size="sm" variant="ghost" disabled={check.isPending} aria-busy={check.isPending} onClick={()=>check.mutate()}>{check.isPending&&<Spinner aria-hidden="true"/>}{reason==="needs_human"&&!hasPendingRequests?t($=>$.supervision.check_again):t($=>$.supervision.check)}</Button>}<ProjectSupervisionSettings project={project} view={view}/></div>}
  </div>
  {view.enabled&&<>
  {categories.some(category=>view.snapshot.counts[category]>0)&&<div className="flex flex-wrap gap-2">{categories.filter(category=>view.snapshot.counts[category]>0).map(category=><Button key={category} size="sm" variant={filter===category?"secondary":"outline"} aria-pressed={filter===category} onClick={()=>setFilter(filter===category?null:category)}>{labels[category]} <span className="tabular-nums">{view.snapshot.counts[category]}</span></Button>)}</div>}
  {reason==="needs_human"&&hasPendingRequests ? <details><summary className="cursor-pointer">{t($=>$.supervision.request_details)}</summary><p className="mt-2 whitespace-pre-wrap break-words">{view.last_result.summary}</p>{view.last_result.wait_reason&&<p className="mt-1 break-words text-muted-foreground">{view.last_result.wait_reason}</p>}</details> : <>{view.last_result.summary&&<p className="break-words">{view.last_result.summary}</p>}{view.last_result.wait_reason&&<p className="break-words text-muted-foreground">{view.last_result.wait_reason}</p>}</>}
  <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">{view.last_checked_at&&<span>{t($=>$.supervision.last_check)} {new Date(view.last_checked_at).toLocaleString(locale)}</span>}{view.enabled&&view.next_check_at&&<span>{t($=>$.supervision.next_check)} {new Date(view.next_check_at).toLocaleString(locale)}</span>}{view.dirty_version>view.handled_version&&<span>{t($=>$.supervision.pending)}</span>}</div>
  {check.isError&&<p role="alert">{t($=>$.supervision.check_failed)}</p>}
  {filter&&view.snapshot.counts[filter]>0&&<div className="space-y-1">{view.snapshot.issues.filter(issue=>issue.category===filter).map(issue=><AppLink key={issue.id} href={paths.issueDetail(issueRouteRef(issue))} className="block truncate rounded-sm px-2 py-1 hover:bg-accent">{issue.identifier} · {issue.title}</AppLink>)}</div>}
  </>}
 </section>;
}
