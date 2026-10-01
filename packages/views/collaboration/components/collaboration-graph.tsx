import { useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { AlertTriangle, CircleDot, GitBranch, Minus, RefreshCw, ZoomIn, ZoomOut } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
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
  readonly onNodeClick?: (node: CollaborationGraphNode) => void;
  readonly onEdgeClick?: (edge: CollaborationGraphEdge) => void;
}

const EDGE_COLORS: Record<string, string> = { delegated: "#2563eb", retry: "#f59e0b", rerun: "#8b5cf6", assigned: "#64748b", produced: "#16a34a", derived_from: "#0f766e", coordinate: "#64748b", handoff: "#2563eb", review: "#f59e0b", accept: "#16a34a" };
const VIEWBOX_WIDTH = 760;
const NODE_WIDTH = 164;
const NODE_HEIGHT = 42;

function statusClass(status: string | null | undefined): string {
  if (status === "running" || status === "working") return "fill-blue-500";
  if (status === "completed" || status === "done") return "fill-emerald-500";
  if (status === "failed" || status === "cancelled") return "fill-red-500";
  return "fill-muted-foreground/50";
}

function edgeTypeLabel(type: CollaborationGraphEdge["type"], labels: Readonly<Record<string, string>>): string {
  return labels[type] ?? type;
}

export function CollaborationGraphCanvas({ nodes, edges, emptyLabel, coverage, coverageReasons, asOf, className, onNodeClick, onEdgeClick }: CollaborationGraphCanvasProps) {
  const { t } = useT("collaboration");
  const edgeLabels: Readonly<Record<string, string>> = {
    delegated: t(($) => $.handoff), retry: t(($) => $.retry), rerun: t(($) => $.rerun), assigned: t(($) => $.coordinate), produced: t(($) => $.handoff), derived_from: t(($) => $.derived_from),
    coordinate: t(($) => $.coordinate), handoff: t(($) => $.handoff), review: t(($) => $.review), accept: t(($) => $.accept),
  };
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{ pointerId: number; x: number; y: number } | null>(null);
  const columns = Math.max(1, Math.ceil(Math.sqrt(nodes.length)));
  const graphWidth = Math.max(VIEWBOX_WIDTH, columns * (NODE_WIDTH + 28));
  const positions = useMemo(() => {
    const columnWidth = graphWidth / columns;
    return new Map(nodes.map((node, index) => [node.id, { x: columnWidth * (index % columns) + columnWidth / 2, y: 46 + Math.floor(index / columns) * 82 }]));
  }, [columns, graphWidth, nodes]);
  const rows = Math.max(1, Math.ceil(nodes.length / columns));
  const height = Math.max(170, 92 + rows * 82);
  const relatedEdges = selectedNode ? edges.filter((edge) => edge.from === selectedNode || edge.to === selectedNode) : [];
  const activeEdge = selectedEdge ? edges.find((edge) => edge.id === selectedEdge) : undefined;

  const onPointerDown = (event: ReactPointerEvent<SVGSVGElement>) => {
    if (event.target !== event.currentTarget) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY };
  };
  const onPointerMove = (event: ReactPointerEvent<SVGSVGElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setPan((current) => ({ x: current.x + event.clientX - drag.x, y: current.y + event.clientY - drag.y }));
    dragRef.current = { ...drag, x: event.clientX, y: event.clientY };
  };
  const stopPointer = (event: ReactPointerEvent<SVGSVGElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) dragRef.current = null;
  };

  if (nodes.length === 0) return <div className={cn("flex min-h-40 items-center justify-center rounded-lg border border-dashed p-6 text-center text-caption text-muted-foreground", className)}><div className="flex flex-col items-center gap-2"><CircleDot className="h-5 w-5" /><span>{emptyLabel}</span></div></div>;

  return <div className={cn("space-y-3", className)}>
    {coverage === "partial" && <div className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-caption text-warning-foreground"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" /><div><p>{t(($) => $.coverage_partial)}</p>{coverageReasons?.length ? <p className="mt-1 text-[11px]">{coverageReasons.join(" · ")}</p> : null}</div></div>}
    <div className="relative overflow-hidden rounded-lg border bg-muted/10 p-2" onWheel={(event) => { event.preventDefault(); setZoom((current) => Math.min(2, Math.max(0.55, current * (event.deltaY < 0 ? 1.08 : 0.92)))); }}>
      <div className="absolute right-3 top-3 z-10 flex gap-1 rounded-md border bg-background/90 p-1 shadow-sm"><button type="button" className="rounded-xs p-1 hover:bg-accent" aria-label="放大" onClick={() => setZoom((current) => Math.min(2, current + 0.1))}><ZoomIn className="h-3.5 w-3.5" /></button><button type="button" className="rounded-xs p-1 hover:bg-accent" aria-label="缩小" onClick={() => setZoom((current) => Math.max(0.55, current - 0.1))}><ZoomOut className="h-3.5 w-3.5" /></button><button type="button" className="rounded-xs px-1 text-[11px] hover:bg-accent" aria-label="重置缩放" onClick={() => { setZoom(1); setPan({ x: 0, y: 0 }); }}>100%</button></div>
      <svg className="min-h-[170px] min-w-[600px] touch-none select-none" width={graphWidth} height={height} viewBox={`0 0 ${graphWidth} ${height}`} role="img" aria-label={t(($) => $.evidence_title)} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={stopPointer} onPointerCancel={stopPointer}>
        <defs><marker id="collaboration-arrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 z" fill="currentColor" /></marker></defs>
        <g transform={`translate(${pan.x} ${pan.y}) scale(${zoom})`}>
          {edges.map((edge) => { const from = positions.get(edge.from); const to = positions.get(edge.to); if (!from || !to) return null; const dx = to.x - from.x, dy = to.y - from.y; const ratio = Math.max(Math.abs(dx) / (NODE_WIDTH / 2 + 4), Math.abs(dy) / (NODE_HEIGHT / 2 + 4), 1); const endX = to.x - dx / ratio, endY = to.y - dy / ratio; const color = EDGE_COLORS[edge.type] ?? "#64748b"; const isSelected = selectedEdge === edge.id; const highlighted = !selectedNode || edge.from === selectedNode || edge.to === selectedNode; return <g key={edge.id} role="button" tabIndex={0} opacity={highlighted ? 1 : 0.18} className="cursor-pointer" onClick={() => { setSelectedEdge(edge.id); onEdgeClick?.(edge); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setSelectedEdge(edge.id); onEdgeClick?.(edge); } }}><line x1={from.x} y1={from.y} x2={endX} y2={endY} stroke="transparent" strokeWidth="12" /><line x1={from.x} y1={from.y} x2={endX} y2={endY} stroke={color} strokeWidth={isSelected ? 4 : Math.min(5, 1 + edge.count / 3)} markerEnd="url(#collaboration-arrow)" /><text x={(from.x + to.x) / 2} y={(from.y + to.y) / 2 - 6} textAnchor="middle" className="fill-muted-foreground text-[10px]">{edgeTypeLabel(edge.type, edgeLabels)} · {edge.count}</text></g>; })}
          {nodes.map((node) => { const point = positions.get(node.id); if (!point) return null; const isSelected = selectedNode === node.id; return <g key={node.id} role="button" tabIndex={0} aria-label={node.label} className="cursor-pointer outline-none" onClick={() => { setSelectedNode(node.id); setSelectedEdge(null); onNodeClick?.(node); }} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setSelectedNode(node.id); setSelectedEdge(null); onNodeClick?.(node); } }}><rect x={point.x - NODE_WIDTH / 2} y={point.y - NODE_HEIGHT / 2} width={NODE_WIDTH} height={NODE_HEIGHT} rx="8" fill="var(--background)" stroke={isSelected ? "var(--foreground)" : "var(--border)"} strokeWidth={isSelected ? 2 : 1} /><circle cx={point.x - NODE_WIDTH / 2 + 16} cy={point.y} r="5" className={statusClass(node.status)} /><text x={point.x - NODE_WIDTH / 2 + 28} y={point.y + 4} fill="var(--foreground)" className="text-[11px]">{node.label.length > 20 ? `${node.label.slice(0, 19)}…` : node.label}</text></g>; })}
        </g>
      </svg>
    </div>
    {(selectedNode || activeEdge) && <div className="rounded-md border bg-background p-3 text-caption" aria-live="polite"><div className="mb-2 flex items-center gap-2 font-medium"><GitBranch className="h-3.5 w-3.5" />{t(($) => $.evidence_title)}</div>{activeEdge ? <div className="space-y-1.5 text-muted-foreground"><p>{edgeTypeLabel(activeEdge.type, edgeLabels)} · {activeEdge.count}</p><p>{t(($) => $.evidence_count, { count: activeEdge.evidence_count })} · {t(($) => $.active_count, { count: activeEdge.active_count })}</p>{activeEdge.last_event_at ? <p>{t(($) => $.last_event, { time: new Date(activeEdge.last_event_at).toLocaleString() })}</p> : null}</div> : relatedEdges.length ? <div className="space-y-1.5 text-muted-foreground">{relatedEdges.map((edge) => <div key={edge.id} className="flex items-center justify-between gap-3"><span>{edgeTypeLabel(edge.type, edgeLabels)}</span><span>{t(($) => $.evidence_count, { count: edge.evidence_count })} · {t(($) => $.active_count, { count: edge.active_count })}</span></div>)}</div> : <p className="text-muted-foreground">{t(($) => $.node_no_edges)}</p>}</div>}
    <div className="flex items-center justify-between gap-3 text-[11px] text-muted-foreground"><span className="flex items-center gap-1"><Minus className="h-3 w-3" />{t(($) => $.footer)}</span>{asOf ? <span className="flex items-center gap-1"><RefreshCw className="h-3 w-3" />{new Date(asOf).toLocaleString()}</span> : null}</div>
  </div>;
}
