import { useEffect, useId, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { ZoomIn, ZoomOut, Expand, RotateCcw } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import type { CollaborationGraphEdge, CollaborationGraphNode } from "@multica/core/types";
import { useT } from "../../i18n";

export interface CollaborationGraphCanvasProps {
 readonly nodes: readonly CollaborationGraphNode[];
 readonly edges: readonly CollaborationGraphEdge[];
 readonly emptyLabel: string;
 readonly coverage?: string;
 readonly coverageReasons?: readonly string[];
 readonly asOf?: string;
 readonly className?: string;
 readonly showEvidence?: boolean;
 readonly showAggregateCounts?: boolean;
 readonly ariaLabel?: string;
 readonly footer?: string;
 readonly onNodeClick?: (node: CollaborationGraphNode) => void;
 readonly onEdgeClick?: (edge: CollaborationGraphEdge) => void;
 readonly onSelectionClear?: () => void;
}
const NODE_WIDTH=180, NODE_HEIGHT=62;
export function CollaborationGraphCanvas({nodes,edges,emptyLabel,coverage,coverageReasons,asOf,className,showEvidence=true,showAggregateCounts=false,ariaLabel,footer,onNodeClick,onEdgeClick,onSelectionClear}:CollaborationGraphCanvasProps){
 const {t}=useT("collaboration");
 const markerId=useId().replace(/:/g,"");
 const canvasRef=useRef<HTMLDivElement>(null);
 const drag=useRef<{x:number;y:number; moved:boolean}|null>(null);
 const [selectedNode,setSelectedNode]=useState<string|null>(null);
 const [selectedEdge,setSelectedEdge]=useState<string|null>(null);
 const [zoom,setZoom]=useState(1);
 const [pan,setPan]=useState({x:0,y:0});
 const hasNodes=nodes.length>0;
 useEffect(()=>{
  const canvas=canvasRef.current;
  if(!canvas)return;
  const wheel=(event:WheelEvent)=>{
   event.preventDefault();
   setZoom(current=>Math.min(2,Math.max(0.2,current*Math.exp(-event.deltaY*0.002))));
  };
  canvas.addEventListener("wheel",wheel,{passive:false});
  return()=>canvas.removeEventListener("wheel",wheel);
 },[hasNodes]);
 const focusedNode=nodes.some(node=>node.id===selectedNode)?selectedNode:null;
 const aggregate=showAggregateCounts || nodes.some(n=>n.data && "task_count" in n.data);
 const columns=Math.max(1,Math.ceil(Math.sqrt(nodes.length)));
 const width=Math.max(760,columns*240);
 const lanes=useMemo(()=>{
  const groups=new Map<string,CollaborationGraphEdge[]>();
  for(const edge of edges){const key=JSON.stringify([edge.from,edge.to].sort());groups.set(key,[...(groups.get(key)??[]),edge])}
  const result=new Map<string,{index:number;count:number}>();
  for(const group of groups.values())group.sort((a,b)=>a.id.localeCompare(b.id)).forEach((edge,index)=>result.set(edge.id,{index,count:group.length}));
  return result;
 },[edges]);
 const laneCount=Math.max(1,...[...lanes.values()].map(lane=>lane.count));
 const topMargin=Math.max(100,70+laneCount*24);
 const height=Math.max(260,Math.ceil(nodes.length/columns)*140+topMargin);
 const positions=useMemo(()=>new Map(nodes.map((node,i)=>[node.id,{x:(i%columns)*240+120,y:Math.floor(i/columns)*140+topMargin}])),[nodes,columns,topMargin]);
 const labels:Record<string,string>={parent_child:t($=>$.parent_child),delegated:t($=>$.delegated),retry:t($=>$.retry),rerun:t($=>$.rerun),coordinate:t($=>$.coordinate),handoff:t($=>$.handoff),review:t($=>$.review),accept:t($=>$.accept),assigned:t($=>$.coordinate),produced:t($=>$.handoff),derived_from:t($=>$.derived_from)};
 const clear=()=>{setSelectedNode(null);setSelectedEdge(null);onSelectionClear?.()};
 const fit=()=>{const available=canvasRef.current?.clientWidth ?? width;setZoom(Math.min(1,Math.max(0.2,(available-24)/width)));setPan({x:0,y:0})};
 const related=(id:string)=>!focusedNode || id===focusedNode || edges.some(e=>(e.from===focusedNode && e.to===id)||(e.to===focusedNode && e.from===id));
 const down=(event:ReactPointerEvent<SVGSVGElement>)=>{if((event.target as Element).closest('[data-graph-control]'))return;event.currentTarget.setPointerCapture(event.pointerId);drag.current={x:event.clientX,y:event.clientY,moved:false}};
 const move=(event:ReactPointerEvent<SVGSVGElement>)=>{if(!drag.current)return;const dx=event.clientX-drag.current.x,dy=event.clientY-drag.current.y;if(Math.abs(dx)+Math.abs(dy)>2)drag.current.moved=true;setPan(p=>({x:p.x+dx,y:p.y+dy}));drag.current={...drag.current,x:event.clientX,y:event.clientY}};
 const up=()=>{if(drag.current && !drag.current.moved)clear();drag.current=null};
 if(!nodes.length)return <div className="rounded-lg border border-dashed p-8 text-center text-caption text-muted-foreground">{emptyLabel}</div>;
 return <div className={cn("space-y-3",className)}>
  {coverage==="partial" && <p role="status" className="rounded-md bg-warning/10 p-2 text-caption">{t($=>$.coverage_partial)} <span>{coverageReasons?.map(reason=>reason==="unresolved_lineage"?t($=>$.source_missing):reason==="scan_limit"?t($=>$.truncated):reason).join(" · ")}</span></p>}
  <div ref={canvasRef} className="relative overflow-hidden rounded-lg border bg-muted/10">
   <div className="absolute right-2 top-2 z-10 flex gap-1 rounded-md border bg-background p-1 text-foreground">
    <Button size="icon-sm" variant="ghost" aria-label={t($=>$.zoom_in)} onClick={()=>setZoom(z=>Math.min(2,z+0.1))}><ZoomIn/></Button>
    <Button size="icon-sm" variant="ghost" aria-label={t($=>$.zoom_out)} onClick={()=>setZoom(z=>Math.max(0.2,z-0.1))}><ZoomOut/></Button>
    <Button size="icon-sm" variant="ghost" aria-label={t($=>$.fit_canvas)} onClick={fit}><Expand/></Button>
    <Button size="icon-sm" variant="ghost" aria-label={t($=>$.reset_canvas)} onClick={()=>{setPan({x:0,y:0});setZoom(1);clear()}}><RotateCcw/></Button>
   </div>
   <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label={ariaLabel ?? t($=>$.evidence_title)} className="touch-none select-none" onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={()=>{drag.current=null}}>
    <defs><marker id={markerId} markerUnits="userSpaceOnUse" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0 0 L8 4 L0 8 Z" fill="context-stroke"/></marker></defs>
    <g transform={`translate(${pan.x} ${pan.y}) scale(${zoom})`}>
     {edges.map(edge=>{
      const from=positions.get(edge.from),to=positions.get(edge.to);if(!from||!to)return null;
      const dx=to.x-from.x,dy=to.y-from.y,len=Math.max(1,Math.hypot(dx,dy));
      const horizontal=Math.abs(dx)>Math.abs(dy);
      const start={x:from.x+(horizontal?Math.sign(dx)*NODE_WIDTH/2:0),y:from.y+(!horizontal?Math.sign(dy)*NODE_HEIGHT/2:0)};
      const end={x:to.x-(horizontal?Math.sign(dx)*NODE_WIDTH/2:0),y:to.y-(!horizontal?Math.sign(dy)*NODE_HEIGHT/2:0)};
      const lane=lanes.get(edge.id);
      const offset=(lane?.count??1)>1?60+20*(lane?.index??0):60;
      const control={x:(from.x+to.x)/2-dy/len*offset,y:(from.y+to.y)/2+dx/len*offset};
      const path=`M ${start.x} ${start.y} Q ${control.x} ${control.y} ${end.x} ${end.y}`;
      const color=edge.source_incomplete?"var(--warning)":aggregate && edge.active_count===0?"var(--muted-foreground)":"var(--info)";
      const text=aggregate?t($=>$.relation_counts,{relation:labels[edge.type]??edge.type,active:edge.active_count,total:edge.count}):`${labels[edge.type]??edge.type} · ${edge.count}`;
      const activate=()=>{setSelectedEdge(edge.id);setSelectedNode(null);onEdgeClick?.(edge)};
      return <g key={edge.id} data-graph-control role="button" aria-label={text} tabIndex={0} opacity={!focusedNode||edge.from===focusedNode||edge.to===focusedNode?1:0.15} className="cursor-pointer focus-visible:outline-ring" onClick={activate} onKeyDown={e=>{if(e.key==="Enter"||e.key===" "){e.preventDefault();activate()}}}>
       <path d={path} fill="none" stroke="transparent" strokeWidth={14}/>
       <path d={path} fill="none" stroke={color} strokeWidth={selectedEdge===edge.id?3:Math.min(3,1+edge.count/8)} strokeDasharray={edge.source_incomplete?"2 4":aggregate&&edge.active_count===0?"6 4":undefined} markerEnd={`url(#${markerId})`}/>
       <rect x={Math.max(120,Math.min(width-120,control.x))-115} y={control.y-24} width={230} height={22} rx={4} fill="var(--background)" />
       <text x={Math.max(120,Math.min(width-120,control.x))} y={control.y-8} textAnchor="middle" className="fill-muted-foreground text-caption"><title>{text}</title>{text}</text>
      </g>
     })}
     {nodes.map(node=>{
      const p=positions.get(node.id);if(!p)return null;
      const taskCount=typeof node.data?.task_count==="number"?node.data.task_count:0;
      const active=typeof node.data?.active_count==="number"?node.data.active_count:0;
      const activate=()=>{setSelectedNode(node.id);setSelectedEdge(null);onNodeClick?.(node)};
      const color=node.status==="running"||node.status==="working"?"var(--info)":node.status==="queued"||node.status==="waiting_local_directory"?"var(--warning)":node.status==="failed"||node.status==="error"?"var(--destructive)":"var(--muted-foreground)";
      return <g key={node.id} data-graph-control role="button" tabIndex={0} aria-label={node.label} opacity={related(node.id)?1:0.2} className="cursor-pointer focus-visible:outline-ring" onClick={activate} onKeyDown={e=>{if(e.key==="Enter"||e.key===" "){e.preventDefault();activate()}}}>
       <rect x={p.x-NODE_WIDTH/2} y={p.y-NODE_HEIGHT/2} width={NODE_WIDTH} height={NODE_HEIGHT} rx={8} fill="var(--background)" stroke={selectedNode===node.id?"var(--foreground)":"var(--border)"} strokeWidth={selectedNode===node.id?2:1}/>
       <circle cx={p.x-72} cy={p.y-8} r={4} fill={color}/>
       <text x={p.x-60} y={p.y-4} className="fill-foreground text-caption"><title>{node.label}</title>{node.label.length>13?`${node.label.slice(0,12)}…`:node.label}</text>
       {aggregate&&<text x={p.x-72} y={p.y+17} className="fill-muted-foreground text-caption">{t($=>$.node_counts,{active,total:taskCount})}</text>}
      </g>
     })}
    </g>
   </svg>
  </div>
  {aggregate&&<p className="text-caption text-muted-foreground">{t($=>$.graph_legend)}</p>}
  {showEvidence && selectedNode && <p className="text-caption text-muted-foreground">{t($=>$.evidence_count,{count:edges.filter(e=>e.from===selectedNode||e.to===selectedNode).reduce((n,e)=>n+e.evidence_count,0)})}</p>}
  <div className="flex flex-wrap justify-between gap-2 text-caption text-muted-foreground"><span>{footer??t($=>$.footer)}</span>{asOf&&<time dateTime={asOf}>{new Date(asOf).toLocaleString()}</time>}</div>
 </div>;
}
