"use client";
import { useCallback, useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Project, Agent, Squad, ProjectExecutionScopeBindings } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useNavigationGuard } from "../../navigation";
import { useT } from "../../i18n";

export function ProjectExecutionScope({project,agents,squads}:{project:Project;agents:Agent[];squads:Squad[]}){
 const {t}=useT("projects");const wsId=useWorkspaceId();const qc=useQueryClient();
 const key=["project-execution-scope-bindings",wsId,project.id];
 const query=useQuery({queryKey:key,queryFn:()=>api.getProjectExecutionScopeBindings(project.id)});
 const [open,setOpen]=useState(false);const [search,setSearch]=useState("");
 const [draft,setDraft]=useState<Pick<ProjectExecutionScopeBindings,"agent_ids"|"squad_ids">|null>(null);
 const save=useMutation({mutationFn:(values:Pick<ProjectExecutionScopeBindings,"agent_ids"|"squad_ids">)=>api.updateProjectExecutionScopeBindings(project.id,values),onSuccess:result=>{qc.setQueryData(key,result);setDraft(null)}});
 const guard=useCallback(()=>!draft || window.confirm(t($=>$.detail.scope_unsaved)),[draft,t]);
 useNavigationGuard(guard);
 useEffect(()=>{if(!draft)return;const prevent=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue=""};window.addEventListener("beforeunload",prevent);return()=>window.removeEventListener("beforeunload",prevent)},[draft]);
 useEffect(()=>{void qc.invalidateQueries({queryKey:["project-execution-scope-bindings",wsId,project.id]})},[project.id,project.lead_id,project.lead_type,qc,wsId]);
 const value=draft??query.data;
 const autoIds=project.lead_type==="agent"&&project.lead_id?[project.lead_id]:[];
 const change=(type:"agent_ids"|"squad_ids",id:string,checked:boolean)=>{if(!value)return;setDraft({agent_ids:value.agent_ids,squad_ids:value.squad_ids,[type]:checked?[...value[type],id]:value[type].filter(existing=>existing!==id)});save.reset()};
 const explicitAgents=agents.filter(a=>!a.archived_at&&!autoIds.includes(a.id)&&a.name.toLowerCase().includes(search.toLowerCase()));
 const filteredSquads=squads.filter(s=>s.name.toLowerCase().includes(search.toLowerCase()));
 const sourceLabels:Record<string,string>={project_lead:t($=>$.detail.execution_scope_lead),explicit:t($=>$.detail.execution_scope_explicit),squad_binding:t($=>$.detail.scope_inherited),workspace_default:t($=>$.detail.scope_workspace)};
 return <section className="mt-4 rounded-md border p-3">
  <button type="button" className="flex w-full flex-wrap items-center justify-between gap-2 text-left text-caption" aria-expanded={open} onClick={()=>setOpen(v=>!v)}><span className="font-medium">{t($=>$.detail.execution_scope)}</span><span className="text-muted-foreground">{t($=>$.detail.scope_summary,{auto:autoIds.length,agents:value?.agent_ids.length??0,squads:value?.squad_ids.length??0})} {open?"⌃":"⌄"}</span></button>
  {open&&<div className="mt-3 space-y-3">
   {query.isPending?<p role="status">{t($=>$.detail.execution_scope_saving)}</p>:query.isError&&!value?<div role="alert">{t($=>$.detail.execution_scope_save_failed)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.detail.scope_retry)}</Button></div>:<>
    {autoIds.length>0&&<div><p className="text-caption font-medium">{t($=>$.detail.execution_scope_auto)}</p>{autoIds.map(id=><label key={id} className="mt-1 flex items-center gap-2 text-caption"><input type="checkbox" checked disabled/>{agents.find(a=>a.id===id)?.name??id}<span className="text-muted-foreground">{t($=>$.detail.execution_scope_lead)}</span></label>)}</div>}
    <label className="block text-caption">{t($=>$.detail.scope_search)}<input type="search" className="mt-1 h-8 w-full rounded-md border bg-background px-2" value={search} onChange={e=>setSearch(e.target.value)}/></label>
    <fieldset disabled={save.isPending} className="max-h-64 space-y-1 overflow-y-auto"><legend className="mb-1 text-caption font-medium">{t($=>$.detail.execution_scope_explicit)}</legend>{explicitAgents.map(a=><label key={a.id} className="flex items-center gap-2 text-caption"><input type="checkbox" checked={value?.agent_ids.includes(a.id)??false} onChange={e=>change("agent_ids",a.id,e.target.checked)}/>{a.name}</label>)}{filteredSquads.map(s=><label key={s.id} className="flex items-center gap-2 text-caption"><input type="checkbox" checked={value?.squad_ids.includes(s.id)??false} onChange={e=>change("squad_ids",s.id,e.target.checked)}/>{s.name}</label>)}</fieldset>
    <details><summary className="cursor-pointer text-caption">{t($=>$.detail.scope_effective,{count:query.data?.effective_agent_ids?.length??0})}</summary><ul className="mt-2 space-y-1 text-caption">{(query.data?.sources??[]).map((source,index)=><li key={source.agent_id+source.source+index}>{agents.find(a=>a.id===source.agent_id)?.name??source.agent_id} · {sourceLabels[source.source]??source.source}{source.squad_id?" · "+(squads.find(s=>s.id===source.squad_id)?.name??source.squad_id):""}</li>)}</ul>{draft&&<p className="mt-1 text-caption text-muted-foreground">{t($=>$.detail.scope_effective_saved)}</p>}</details>
    {save.isError&&<p role="alert" className="text-caption text-destructive">{t($=>$.detail.scope_save_preserved)}</p>}
    <div className="flex gap-2"><Button size="sm" disabled={!draft||save.isPending} aria-busy={save.isPending} onClick={()=>{if(draft)save.mutate(draft)}}>{save.isPending?t($=>$.detail.execution_scope_saving):t($=>$.detail.execution_scope_save)}</Button>{draft&&<Button size="sm" variant="outline" disabled={save.isPending} onClick={()=>{setDraft(null);save.reset()}}>{t($=>$.detail.execution_scope_cancel)}</Button>}</div>
   </>}
  </div>}
 </section>;
}
