"use client";
import { useMemo, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { projectCollaborationEvidenceInfiniteOptions, type ProjectCollaborationFilters } from "@multica/core/collaboration";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { api } from "@multica/core/api";
import type { CollaborationGraphNode, CollaborationGraphEdge, ProjectCollaborationGraphResponse, ProjectCollaborationEvidence, ProjectCollaborationEvidenceResponse } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@multica/ui/components/ui/sheet";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

type Issue = NonNullable<ProjectCollaborationEvidenceResponse["issues"]>[number];
export function CollaborationEvidenceDrawer({projectId,filters,selection,graph,onClose}:{projectId:string;filters:ProjectCollaborationFilters;selection:{node?:CollaborationGraphNode;edge?:CollaborationGraphEdge};graph?:ProjectCollaborationGraphResponse;onClose:()=>void}){
 const {t}=useT("collaboration");
 const wsId=useWorkspaceId();const paths=useWorkspacePaths();
 const [sort,setSort]=useState<"newest"|"oldest">("newest");
 const [status,setStatus]=useState("");
 const edge=selection.edge;const node=selection.node;
 const evidenceFilters={...filters,node_agent_id:node?.id.replace(/^agent:/,""),sort,status:filters.status||status||undefined};
 // A node selection requests all of its incoming and outgoing runs.
 const query=useInfiniteQuery(projectCollaborationEvidenceInfiniteOptions(wsId,projectId,edge?.id,evidenceFilters,true));
 const evidence=useMemo(()=>[...new Map((query.data?.pages??[]).flatMap(p=>p.evidence).map(e=>[e.task_id,e])).values()],[query.data]);
 const issues=useMemo(()=>{
  const merged=new Map<string,Issue>();
  for(const page of query.data?.pages??[])for(const issue of page.issues??[]){const prev=merged.get(issue.id);merged.set(issue.id,{...issue,context_only:issue.context_only&&(prev?.context_only??true)})}
  for(const run of evidence)if(run.issue_id&&!merged.has(run.issue_id))merged.set(run.issue_id,{id:run.issue_id,title:run.issue_title??"",key:run.issue_key??"",status:run.issue_status??"",parent_issue_id:null,context_only:false});
  return merged;
 },[query.data,evidence]);
 const byIssue=new Map<string,ProjectCollaborationEvidence[]>();
 for(const run of evidence){const key=run.issue_id??"standalone";byIssue.set(key,[...(byIssue.get(key)??[]),run])}
 const renderIssue=(issue:Issue,ancestors:Set<string>=new Set()):React.ReactNode=>{
  if(ancestors.has(issue.id))return null;
  const path=new Set(ancestors);path.add(issue.id);
  const children=[...issues.values()].filter(i=>i.parent_issue_id===issue.id).sort((a,b)=>compareIssues(a,b));
  const runs=byIssue.get(issue.id)??[];
  return <details key={issue.id} open className="rounded-md border p-3">
   <summary className={"cursor-pointer text-caption font-medium"+(issue.context_only?" text-muted-foreground":"")}><span>{issue.key} · {issue.title}</span> <span className="text-muted-foreground">{issue.status}</span>{issue.context_only&&<span className="ml-2 text-muted-foreground">{t($=>$.context_task)}</span>}</summary>
   <div className="mt-2 space-y-2"><AppLink className="text-caption underline" href={paths.issueDetail(issue.key||issue.id)}>{t($=>$.open_task)}</AppLink>
    {runs.map(run=><CollaborationRun key={run.task_id} projectId={projectId} run={run}/>)}
    {children.length>0&&<div className="ml-3 space-y-2 border-l pl-3">{children.map(child=>renderIssue(child,path))}</div>}
   </div>
  </details>;
 };
 const roots=[...issues.values()].filter(i=>!i.parent_issue_id||!issues.has(i.parent_issue_id));
 if(!roots.length&&issues.size)roots.push(...issues.values());
 const issueRank=(id:string,visited=new Set<string>()):number=>{
  if(visited.has(id))return sort==="newest"?-Infinity:Infinity;
  const next=new Set(visited);next.add(id);
  const times=(byIssue.get(id)??[]).map(run=>Date.parse(run.created_at));
  for(const child of issues.values())if(child.parent_issue_id===id)times.push(issueRank(child.id,next));
  return sort==="newest"?Math.max(-Infinity,...times):Math.min(Infinity,...times);
 };
 const compareIssues=(a:Issue,b:Issue)=>{
  const left=issueRank(a.id),right=issueRank(b.id);
  if(left===right)return a.key.localeCompare(b.key)||a.id.localeCompare(b.id);
  return sort==="newest"?right-left:left-right;
 };
 roots.sort(compareIssues);
 const related=(graph?.edges??[]).filter(e=>!node||e.from===node.id||e.to===node.id);
 const title=edge?`${graph?.nodes.find(n=>n.id===edge.from)?.label??edge.from} → ${graph?.nodes.find(n=>n.id===edge.to)?.label??edge.to}`:node?.label??t($=>$.evidence_title);
 return <Sheet open onOpenChange={open=>{if(!open)onClose()}}>
  <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl" aria-describedby={undefined}>
   <SheetHeader><SheetTitle>{title}</SheetTitle></SheetHeader>
   <div className="space-y-3 px-4 pb-6">
    {node&&<p className="text-caption">{t($=>$.node_counts,{active:typeof node.data?.active_count==="number"?node.data.active_count:0,total:typeof node.data?.task_count==="number"?node.data.task_count:0})}</p>}
    {node&&<p className="text-caption text-muted-foreground">{t($=>$.run_summary,{queued:Number(node.data?.queued_count??0),running:Number(node.data?.running_count??0),failed:Number(node.data?.failed_count??0),completed:Number(node.data?.completed_count??0)})}</p>}
    <p className="text-caption text-muted-foreground">{t($=>$.evidence_count,{count:query.data?.pages[0]?.total??0})}</p>
    {node&&related.length>0&&<details open><summary className="text-caption font-medium">{t($=>$.relations_title)}</summary><ul className="mt-2 space-y-1 text-caption">{related.map(e=><li key={e.id}>{graph?.nodes.find(n=>n.id===e.from)?.label} → {graph?.nodes.find(n=>n.id===e.to)?.label} · {e.active_count}/{e.count}</li>)}</ul></details>}
    <label className="text-caption">{t($=>$.sort)} <select className="rounded-md border bg-background px-2 py-1" value={sort} onChange={e=>setSort(e.target.value as "newest"|"oldest")}><option value="newest">{t($=>$.newest)}</option><option value="oldest">{t($=>$.oldest)}</option></select></label>
    <label className="block text-caption">{t($=>$.status_filter)} <select className="rounded-md border bg-background px-2 py-1" disabled={Boolean(filters.status)} value={filters.status||status} onChange={e=>setStatus(e.target.value)}><option value="">{t($=>$.status_all)}</option>{["queued","dispatched","running","waiting_local_directory","completed","failed","cancelled"].map(value=><option key={value} value={value}>{t($=>$.run_statuses[value as keyof typeof $.run_statuses])}</option>)}</select></label>
    {query.isPending?<p role="status">{t($=>$.loading_more)}</p>:query.isError?<div role="alert">{t($=>$.load_failed)} <Button variant="outline" size="sm" onClick={()=>void query.refetch()}>{t($=>$.retry)}</Button></div>:evidence.length?<div className="space-y-3">{roots.map(i=>renderIssue(i))}{(byIssue.get("standalone")??[]).map(run=><CollaborationRun key={run.task_id} projectId={projectId} run={run}/>)}</div>:<p>{t($=>$.no_evidence)}</p>}
    {query.data?.pages[0]?.truncated&&<p role="status" className="text-caption text-muted-foreground">{t($=>$.truncated)}</p>}
    {query.hasNextPage&&<Button variant="outline" size="sm" disabled={query.isFetchingNextPage} onClick={()=>void query.fetchNextPage()}>{t($=>$.load_more)}</Button>}
   </div>
  </SheetContent>
 </Sheet>;
}
function CollaborationRun({run,projectId,allowSource=true}:{run:ProjectCollaborationEvidence;projectId:string;allowSource?:boolean}){
 const {t}=useT("collaboration");const paths=useWorkspacePaths();
 const statusLabels:Record<string,string>={queued:t($=>$.run_statuses.queued),dispatched:t($=>$.run_statuses.dispatched),running:t($=>$.run_statuses.running),waiting_local_directory:t($=>$.run_statuses.waiting_local_directory),completed:t($=>$.run_statuses.completed),failed:t($=>$.run_statuses.failed),cancelled:t($=>$.run_statuses.cancelled)};
 const relationLabels:Record<string,string>={parent_child:t($=>$.parent_child),delegated:t($=>$.delegated),retry:t($=>$.retry),rerun:t($=>$.rerun),root:t($=>$.root)};
 const [error,setError]=useState(false);const [downloading,setDownloading]=useState(false);
 const download=async(id:string,name:string)=>{setDownloading(true);setError(false);try{const blob=await api.getAttachmentBlob(id);const url=URL.createObjectURL(blob);const anchor=document.createElement("a");anchor.href=url;anchor.download=name;anchor.click();URL.revokeObjectURL(url)}catch{setError(true)}finally{setDownloading(false)}};
 return <details className="rounded-md bg-muted/30 p-2">
  <summary className="cursor-pointer text-caption">{run.agent_name||run.agent_id} · {statusLabels[run.status]??run.status} · {run.task_id.slice(0,8)}</summary>
  <div className="mt-2 space-y-2 break-words text-caption text-muted-foreground">
   <p>{t($=>$.task_events,{id:run.task_id,count:run.event_count})}</p>
   <p>{t($=>$.started)} {run.started_at?new Date(run.started_at).toLocaleString():"—"} · {t($=>$.finished)} {run.completed_at?new Date(run.completed_at).toLocaleString():"—"}</p>
   {run.source_task_id?<><p>{t($=>$.source_task,{id:run.source_task_id})} · {run.source_agent_name} · {relationLabels[run.relation_type]??run.relation_type}</p>{allowSource&&<SourceRunPreview projectId={projectId} runId={run.source_task_id}/>}</>:run.relation_type==="unknown"?<p>{t($=>$.source_missing)}</p>:null}
   {run.source_issue_id&&<AppLink className="underline" href={paths.issueDetail(run.source_issue_id)}>{t($=>$.open_task)} · {run.source_agent_name}</AppLink>}
   {run.issue_id&&run.trigger_comment_id&&<AppLink className="underline" href={paths.issueDetail(run.issue_key||run.issue_id)+"#comment-"+run.trigger_comment_id}>{t($=>$.open_trigger)}</AppLink>}
   {(run.artifacts??[]).map(artifact=><Button key={artifact.id} variant="outline" size="sm" disabled={downloading} onClick={()=>void download(artifact.id,artifact.filename)}>{artifact.filename}</Button>)}
   {(run.events??[]).map(event=><p key={event.id}>{event.event_type} · {new Date(event.created_at).toLocaleString()}</p>)}
   {error&&<p role="alert" className="text-destructive">{t($=>$.download_failed)}</p>}
  </div>
 </details>;
}

function SourceRunPreview({projectId,runId}:{projectId:string;runId:string}){
 const {t}=useT("collaboration");const wsId=useWorkspaceId();const paths=useWorkspacePaths();
 const [open,setOpen]=useState(false);
 const query=useQuery({queryKey:["collaboration-graph","source-run",wsId,projectId,runId],queryFn:()=>api.getProjectCollaborationEvidence(projectId,{activity:"all",run_id:runId,limit:1}),enabled:open,staleTime:15_000,refetchInterval:open?15_000:false});
 const source=query.data?.evidence[0];
 return <div className="space-y-2"><Button size="sm" variant="outline" aria-expanded={open} onClick={()=>setOpen(current=>!current)}>{t($=>$.open_source_run)}</Button>{open&&(query.isPending?<p role="status">{t($=>$.loading_more)}</p>:query.isError?<div role="alert">{t($=>$.load_failed)} <Button size="sm" variant="outline" onClick={()=>void query.refetch()}>{t($=>$.retry)}</Button></div>:source?<div className="space-y-2 rounded-md border p-2">{source.issue_id&&<AppLink className="underline" href={paths.issueDetail(source.issue_key||source.issue_id)}>{source.issue_key} · {source.issue_title}</AppLink>}<CollaborationRun projectId={projectId} run={source} allowSource={false}/></div>:<p>{t($=>$.source_missing)}</p>)}</div>;
}
