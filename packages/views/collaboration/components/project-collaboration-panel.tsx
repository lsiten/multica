"use client";
import { useEffect, useId, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectCollaborationGraphOptions, type ProjectCollaborationFilters } from "@multica/core/collaboration";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Agent, Squad } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { useSearchState } from "../../navigation";
import { CollaborationGraphCanvas } from "./collaboration-graph";
import { CollaborationEvidenceDrawer } from "./collaboration-evidence-drawer";
import {
  COLLABORATION_RUN_STATUSES,
  collaborationActivityFromParam,
  collaborationDateBoundary,
  collaborationStatusFromParam,
  collaborationViewFromParam,
} from "./collaboration-filter-state";

export function ProjectCollaborationPanel({projectId,agents,squads}:{projectId:string;agents:Agent[];squads:Squad[]}){
 const {t}=useT("collaboration");
 const wsId=useWorkspaceId();
 const [activityParam,setActivityParam]=useSearchState("collab_activity","active");
 const [statusParam,setStatusParam]=useSearchState("collab_status");
 const [agentParam,setAgentParam]=useSearchState("collab_agent");
 const [squadParam,setSquadParam]=useSearchState("collab_squad");
 const [relationParam,setRelationParam]=useSearchState("collab_relation");
 const [taskQuery,setTaskQuery]=useSearchState("collab_q");
 const [fromDate,setFromDate]=useSearchState("collab_from");
 const [toDate,setToDate]=useSearchState("collab_to");
 const [independentParam,setIndependentParam]=useSearchState("collab_independent");
 const [viewParam,setViewParam]=useSearchState("collab_view","graph");
 const collaborationView=collaborationViewFromParam(viewParam);
 const showIndependent=independentParam==="1";
 const filters=useMemo<ProjectCollaborationFilters>(()=>({
   activity:collaborationActivityFromParam(activityParam),
   status:collaborationStatusFromParam(statusParam)||undefined,
   agent_id:agentParam||undefined,
   squad_id:squadParam||undefined,
   relation_type:relationParam||undefined,
   task_query:taskQuery||undefined,
   from:collaborationDateBoundary(fromDate,false),
   to:collaborationDateBoundary(toDate,true),
 }),[activityParam,statusParam,agentParam,squadParam,relationParam,taskQuery,fromDate,toDate]);
 const [expanded,setExpanded]=useState(false);
 const [selection,setSelection]=useState<{nodeId?:string;edgeId?:string}|null>(null);
 const query=useQuery(projectCollaborationGraphOptions(wsId,projectId,filters));
 const graph=query.data;
 const resetView=()=>{setExpanded(false);setSelection(null)};
 useEffect(()=>{setSelection(null);setExpanded(false)},[wsId,projectId]);
 const selectedNode=graph?.nodes.find(node=>node.id===selection?.nodeId);
 const selectedEdge=graph?.edges.find(edge=>edge.id===selection?.edgeId);
 const selected=selectedNode?{node:selectedNode}:selectedEdge?{edge:selectedEdge}:null;
 const edges=graph?.edges ?? [];
 const visibleEdges=expanded?edges:edges.slice(0,20);
 const linked=new Set(visibleEdges.flatMap(e=>[e.from,e.to]));
 const nodes=(graph?.nodes ?? []).filter(n=>showIndependent||linked.has(n.id));
 const inputClass="h-8 min-w-0 rounded-md border bg-background px-2 text-caption";
 const relationLabels:Record<string,string>={parent_child:t($=>$.parent_child),delegated:t($=>$.delegated),retry:t($=>$.retry),rerun:t($=>$.rerun),root:t($=>$.root)};
 const graphTitleId=useId();
 const relationListTitleId=useId();
 return <section className="space-y-4">
  <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-body font-medium">{t($=>$.project_title)}</h2><div className="flex items-center gap-2"><div role="group" aria-label={t($=>$.project_title)} className="flex rounded-md bg-muted p-0.5"><Button size="sm" variant="ghost" aria-pressed={collaborationView==="graph"} className={collaborationView==="graph"?"bg-background text-foreground shadow-xs":""} onClick={()=>setViewParam("graph")}>{t($=>$.view_graph)}</Button><Button size="sm" variant="ghost" aria-pressed={collaborationView==="list"} className={collaborationView==="list"?"bg-background text-foreground shadow-xs":""} onClick={()=>setViewParam("list")}>{t($=>$.view_list)}</Button></div><Button size="sm" variant="outline" disabled={query.isFetching} onClick={()=>void query.refetch()}>{t($=>$.refresh)}</Button></div></div>
  <div className="flex flex-wrap gap-2">
   <label className="text-caption">{t($=>$.activity_filter)} <select aria-label={t($=>$.activity_filter)} className={inputClass} value={filters.activity??"active"} onChange={e=>{setActivityParam(e.target.value);resetView()}}><option value="active">{t($=>$.activity_active)}</option><option value="all">{t($=>$.activity_all)}</option><option value="ended">{t($=>$.activity_ended)}</option></select></label>
   <label className="text-caption">{t($=>$.status_filter)} <select aria-label={t($=>$.status_filter)} className={inputClass} value={filters.status??""} onChange={e=>{setStatusParam(e.target.value);resetView()}}><option value="">{t($=>$.status_all)}</option>{COLLABORATION_RUN_STATUSES.map(status=><option key={status} value={status}>{t($=>$.run_statuses[status])}</option>)}</select></label>
   <label className="text-caption">{t($=>$.agent_filter)} <select aria-label={t($=>$.agent_filter)} className={inputClass+" max-w-44"} value={filters.agent_id??""} onChange={e=>{setAgentParam(e.target.value);resetView()}}><option value="">{t($=>$.all_agents)}</option>{agents.map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label>
   <label className="text-caption">{t($=>$.squad_filter)} <select aria-label={t($=>$.squad_filter)} className={inputClass+" max-w-44"} value={filters.squad_id??""} onChange={e=>{setSquadParam(e.target.value);resetView()}}><option value="">{t($=>$.all_squads)}</option>{squads.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select></label>
   <label className="text-caption">{t($=>$.relation_filter)} <select aria-label={t($=>$.relation_filter)} className={inputClass} value={filters.relation_type??""} onChange={e=>{setRelationParam(e.target.value);resetView()}}><option value="">{t($=>$.all_relations)}</option><option value="delegated">{t($=>$.delegated)}</option><option value="retry">{t($=>$.retry)}</option><option value="rerun">{t($=>$.rerun)}</option><option value="parent_child">{t($=>$.parent_child)}</option><option value="root">{t($=>$.root)}</option></select></label>
   <label className="text-caption">{t($=>$.task_filter)} <input type="search" aria-label={t($=>$.task_filter)} className={inputClass} value={taskQuery} placeholder={t($=>$.task_filter_hint)} onChange={e=>{setTaskQuery(e.target.value);resetView()}}/></label>
   <label className="text-caption">{t($=>$.from_date)} <input type="date" aria-label={t($=>$.from_date)} className={inputClass} value={fromDate} onChange={e=>{setFromDate(e.target.value);resetView()}}/></label>
   <label className="text-caption">{t($=>$.to_date)} <input type="date" aria-label={t($=>$.to_date)} className={inputClass} min={fromDate} value={toDate} onChange={e=>{setToDate(e.target.value);resetView()}}/></label>
  </div>
  <div className="flex flex-wrap items-center gap-4 text-caption">
   <label className="flex items-center gap-2"><input type="checkbox" checked={showIndependent} onChange={e=>setIndependentParam(e.target.checked?"1":"")}/>{t($=>$.show_independent)}</label>
   {graph&&<span className="text-muted-foreground">{t($=>$.graph_summary,{active:graph.summary.active_count,total:graph.summary.task_count,agents:graph.summary.agent_count})}{graph.summary.run_count!==undefined&&" · "+t($=>$.run_count,{count:graph.summary.run_count})}</span>}
  </div>
  {query.isPending?<p role="status">{t($=>$.loading_more)}</p>:query.isError?<div role="alert" className="text-caption text-destructive">{t($=>$.load_failed)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.retry)}</Button></div>:
   collaborationView==="graph"?<CollaborationGraphCanvas key={JSON.stringify(filters)} nodes={nodes} edges={visibleEdges} showAggregateCounts showEvidence={false} coverage={graph?.summary.coverage} coverageReasons={graph?.coverage_reasons} asOf={graph?.as_of} emptyLabel={t($=>$.empty_project)} onNodeClick={node=>setSelection({nodeId:node.id})} onEdgeClick={edge=>setSelection({edgeId:edge.id})} onSelectionClear={()=>setSelection(null)}/>:
   <div className="space-y-4 rounded-lg border p-3">
    <section className="space-y-2"><h3 id={graphTitleId} className="text-caption font-medium">{t($=>$.accessible_nodes)}</h3><ul aria-labelledby={graphTitleId} className="divide-y rounded-md border">{nodes.length?nodes.map(node=>{
      const active=typeof node.data?.active_count==="number"?node.data.active_count:0;
      const total=typeof node.data?.task_count==="number"?node.data.task_count:0;
      return <li key={node.id}><Button variant="ghost" className="h-auto w-full justify-between gap-3 rounded-none px-3 py-2 text-left" onClick={()=>setSelection({nodeId:node.id})}><span className="min-w-0 break-words">{node.label}</span><span className="shrink-0 text-caption text-muted-foreground">{t($=>$.node_counts,{active,total})}</span></Button></li>;
    }):<li className="px-3 py-6 text-center text-caption text-muted-foreground">{t($=>$.empty_project)}</li>}</ul></section>
    <section className="space-y-2"><h3 id={relationListTitleId} className="text-caption font-medium">{t($=>$.accessible_relations)}</h3><ul aria-labelledby={relationListTitleId} className="divide-y rounded-md border">{visibleEdges.length?visibleEdges.map(edge=>{
      const from=graph?.nodes.find(node=>node.id===edge.from)?.label??edge.from;
      const to=graph?.nodes.find(node=>node.id===edge.to)?.label??edge.to;
      return <li key={edge.id}><Button variant="ghost" className="h-auto w-full justify-between gap-3 rounded-none px-3 py-2 text-left" onClick={()=>setSelection({edgeId:edge.id})}><span className="min-w-0 break-words">{from} → {to} · {relationLabels[edge.type]??edge.type}</span><span className="shrink-0 text-caption text-muted-foreground">{t($=>$.node_counts,{active:edge.active_count,total:edge.count})}</span></Button></li>;
    }):<li className="px-3 py-6 text-center text-caption text-muted-foreground">{t($=>$.no_relations)}</li>}</ul></section>
   </div>}
  {!showIndependent && !nodes.length && (graph?.nodes.length??0)>0 && <Button variant="outline" size="sm" onClick={()=>setIndependentParam("1")}>{t($=>$.show_independent)}</Button>}
  {edges.length>20&&!expanded&&<Button variant="outline" size="sm" onClick={()=>setExpanded(true)}>{t($=>$.expand_relations,{count:edges.length-20})}</Button>}
  {expanded&&edges.length>20&&<Button variant="ghost" size="sm" onClick={()=>setExpanded(false)}>{t($=>$.collapse_relations)}</Button>}
  {graph?.truncated&&<p role="status" className="text-caption text-muted-foreground">{t($=>$.truncated)}</p>}
  {selected&&<CollaborationEvidenceDrawer projectId={projectId} filters={filters} selection={selected} graph={graph} onClose={()=>setSelection(null)}/>}
 </section>;
}
