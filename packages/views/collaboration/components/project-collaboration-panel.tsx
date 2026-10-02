"use client";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectCollaborationGraphOptions, type ProjectCollaborationFilters } from "@multica/core/collaboration";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Agent, Squad } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { CollaborationGraphCanvas } from "./collaboration-graph";
import { CollaborationEvidenceDrawer } from "./collaboration-evidence-drawer";

export function ProjectCollaborationPanel({projectId,agents,squads}:{projectId:string;agents:Agent[];squads:Squad[]}){
 const {t}=useT("collaboration");
 const wsId=useWorkspaceId();
 const [filters,setFilters]=useState<ProjectCollaborationFilters>({activity:"active"});
 const [showIndependent,setShowIndependent]=useState(false);
 const [expanded,setExpanded]=useState(false);
 const [fromDate,setFromDate]=useState("");const [toDate,setToDate]=useState("");
 const [selection,setSelection]=useState<{nodeId?:string;edgeId?:string}|null>(null);
 const query=useQuery(projectCollaborationGraphOptions(wsId,projectId,filters));
 const graph=query.data;
 const setFilter=(patch:Partial<ProjectCollaborationFilters>)=>{setFilters(f=>({...f,...patch}));setExpanded(false);setSelection(null)};
 useEffect(()=>{setSelection(null);setExpanded(false)},[wsId,projectId]);
 const selectedNode=graph?.nodes.find(node=>node.id===selection?.nodeId);
 const selectedEdge=graph?.edges.find(edge=>edge.id===selection?.edgeId);
 const selected=selectedNode?{node:selectedNode}:selectedEdge?{edge:selectedEdge}:null;
 const edges=graph?.edges ?? [];
 const visibleEdges=expanded?edges:edges.slice(0,20);
 const linked=new Set(visibleEdges.flatMap(e=>[e.from,e.to]));
 const nodes=(graph?.nodes ?? []).filter(n=>showIndependent||linked.has(n.id));
 const inputClass="h-8 min-w-0 rounded-md border bg-background px-2 text-caption";
 return <section className="space-y-4">
  <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-body font-medium">{t($=>$.project_title)}</h2><Button size="sm" variant="outline" disabled={query.isFetching} onClick={()=>void query.refetch()}>{t($=>$.refresh)}</Button></div>
  <div className="flex flex-wrap gap-2">
   <label className="text-caption">{t($=>$.activity_filter)} <select aria-label={t($=>$.activity_filter)} className={inputClass} value={filters.activity??"active"} onChange={e=>setFilter({activity:e.target.value as "active"|"all"|"ended"})}><option value="active">{t($=>$.activity_active)}</option><option value="all">{t($=>$.activity_all)}</option><option value="ended">{t($=>$.activity_ended)}</option></select></label>
   <label className="text-caption">{t($=>$.status_filter)} <select aria-label={t($=>$.status_filter)} className={inputClass} value={filters.status??""} onChange={e=>setFilter({status:e.target.value||undefined})}><option value="">{t($=>$.status_all)}</option>{["queued","dispatched","running","waiting_local_directory","completed","failed","cancelled"].map(status=><option key={status} value={status}>{t($=>$.run_statuses[status as keyof typeof $.run_statuses])}</option>)}</select></label>
   <label className="text-caption">{t($=>$.agent_filter)} <select aria-label={t($=>$.agent_filter)} className={inputClass+" max-w-44"} value={filters.agent_id??""} onChange={e=>setFilter({agent_id:e.target.value||undefined})}><option value="">{t($=>$.all_agents)}</option>{agents.map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label>
   <label className="text-caption">{t($=>$.squad_filter)} <select aria-label={t($=>$.squad_filter)} className={inputClass+" max-w-44"} value={filters.squad_id??""} onChange={e=>setFilter({squad_id:e.target.value||undefined})}><option value="">{t($=>$.all_squads)}</option>{squads.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select></label>
   <label className="text-caption">{t($=>$.relation_filter)} <select aria-label={t($=>$.relation_filter)} className={inputClass} value={filters.relation_type??""} onChange={e=>setFilter({relation_type:e.target.value||undefined})}><option value="">{t($=>$.all_relations)}</option><option value="delegated">{t($=>$.delegated)}</option><option value="retry">{t($=>$.retry)}</option><option value="rerun">{t($=>$.rerun)}</option><option value="parent_child">{t($=>$.parent_child)}</option><option value="root">{t($=>$.root)}</option></select></label>
   <label className="text-caption">{t($=>$.task_filter)} <input type="search" aria-label={t($=>$.task_filter)} className={inputClass} value={filters.task_query??""} placeholder={t($=>$.task_filter_hint)} onChange={e=>setFilter({task_query:e.target.value||undefined})}/></label>
   <label className="text-caption">{t($=>$.from_date)} <input type="date" aria-label={t($=>$.from_date)} className={inputClass} value={fromDate} onChange={e=>{setFromDate(e.target.value);setFilter({from:e.target.value?new Date(e.target.value+"T00:00:00").toISOString():undefined})}}/></label>
   <label className="text-caption">{t($=>$.to_date)} <input type="date" aria-label={t($=>$.to_date)} className={inputClass} min={fromDate} value={toDate} onChange={e=>{setToDate(e.target.value);setFilter({to:e.target.value?new Date(e.target.value+"T23:59:59.999").toISOString():undefined})}}/></label>
  </div>
  <div className="flex flex-wrap items-center gap-4 text-caption">
   <label className="flex items-center gap-2"><input type="checkbox" checked={filters.activity==="active"} onChange={e=>setFilter({activity:e.target.checked?"active":"all"})}/>{t($=>$.only_active)}</label>
   <label className="flex items-center gap-2"><input type="checkbox" checked={showIndependent} onChange={e=>setShowIndependent(e.target.checked)}/>{t($=>$.show_independent)}</label>
   {graph&&<span className="text-muted-foreground">{t($=>$.graph_summary,{active:graph.summary.active_count,total:graph.summary.task_count,agents:graph.summary.agent_count})}{graph.summary.run_count!==undefined&&" · "+t($=>$.run_count,{count:graph.summary.run_count})}</span>}
  </div>
  {query.isPending?<p role="status">{t($=>$.loading_more)}</p>:query.isError?<div role="alert" className="text-caption text-destructive">{t($=>$.load_failed)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.retry)}</Button></div>:
   <CollaborationGraphCanvas key={JSON.stringify(filters)} nodes={nodes} edges={visibleEdges} showAggregateCounts showEvidence={false} coverage={graph?.summary.coverage} coverageReasons={graph?.coverage_reasons} asOf={graph?.as_of} emptyLabel={t($=>$.empty_project)} onNodeClick={node=>setSelection({nodeId:node.id})} onEdgeClick={edge=>setSelection({edgeId:edge.id})} onSelectionClear={()=>setSelection(null)}/>}
  {!showIndependent && !nodes.length && (graph?.nodes.length??0)>0 && <Button variant="outline" size="sm" onClick={()=>setShowIndependent(true)}>{t($=>$.show_independent)}</Button>}
  {edges.length>20&&!expanded&&<Button variant="outline" size="sm" onClick={()=>setExpanded(true)}>{t($=>$.expand_relations,{count:edges.length-20})}</Button>}
  {expanded&&edges.length>20&&<Button variant="ghost" size="sm" onClick={()=>setExpanded(false)}>{t($=>$.collapse_relations)}</Button>}
  {graph?.truncated&&<p role="status" className="text-caption text-muted-foreground">{t($=>$.truncated)}</p>}
  {selected&&<CollaborationEvidenceDrawer projectId={projectId} filters={filters} selection={selected} graph={graph} onClose={()=>setSelection(null)}/>}
 </section>;
}
