// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import type { CollaborationGraphEdge, CollaborationGraphNode } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { CollaborationGraphCanvas } from "./collaboration-graph";

const nodes: readonly CollaborationGraphNode[] = [
  { id: "agent-a", type: "agent", label: "Coordinator", status: "working" },
  { id: "agent-b", type: "agent", label: "Builder", status: "done" },
];
const edges: readonly CollaborationGraphEdge[] = [{ id: "edge-a", from: "agent-a", to: "agent-b", type: "delegated", count: 2, active_count: 1, evidence_count: 2, last_event_at: "2026-09-30T00:00:00Z" }];

describe("CollaborationGraphCanvas", () => {
  beforeEach(() => {
    vi.stubGlobal("PointerEvent", class extends MouseEvent {
      readonly pointerId: number;
      constructor(type: string, options: PointerEventInit = {}) {
        super(type, options);
        this.pointerId = options.pointerId ?? 1;
      }
    });
    SVGElement.prototype.setPointerCapture = vi.fn();
    SVGElement.prototype.hasPointerCapture = vi.fn(() => true);
    SVGElement.prototype.releasePointerCapture = vi.fn();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("drags a node and its edge without panning or opening evidence, then resets its position", () => {
    const onNodeClick = vi.fn();
    const onSelectionClear = vi.fn();
    const { container, rerender } = renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" onNodeClick={onNodeClick} onSelectionClear={onSelectionClear} />);
    const node = screen.getByRole("button", { name: "Coordinator" });
    const rect = node.querySelector("rect")!;
    const otherRect = screen.getByRole("button", { name: "Builder" }).querySelector("rect")!;
    const edge = container.querySelector("path[marker-end]")!;
    const initialPath = edge.getAttribute("d");
    fireEvent.pointerDown(rect, { clientX: 120, clientY: 100, button: 0 });
    fireEvent.pointerMove(node, { clientX: 180, clientY: 140 });
    fireEvent.pointerUp(node, { clientX: 180, clientY: 140 });
    fireEvent.click(node);
    expect(rect).toHaveAttribute("x", "90");
    expect(rect).toHaveAttribute("y", "109");
    expect(otherRect).toHaveAttribute("x", "270");
    expect(container.querySelector("g[transform]")).toHaveAttribute("transform", "translate(0 0) scale(1)");
    expect(edge.getAttribute("d")).not.toBe(initialPath);
    expect(onNodeClick).not.toHaveBeenCalled();
    expect(onSelectionClear).not.toHaveBeenCalled();
    rerender(<CollaborationGraphCanvas nodes={[...nodes]} edges={[...edges]} emptyLabel="No activity" />);
    expect(rect).toHaveAttribute("x", "90");
    fireEvent.click(screen.getByRole("button", { name: "Reset canvas" }));
    expect(rect).toHaveAttribute("x", "30");
    expect(edge).toHaveAttribute("d", initialPath!);
  });

  it("accounts for zoom while dragging and stops on pointer cancellation", () => {
    renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" />);
    fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
    const node = screen.getByRole("button", { name: "Coordinator" });
    const rect = node.querySelector("rect")!;
    fireEvent.pointerDown(node, { clientX: 120, clientY: 100, button: 0 });
    fireEvent.pointerMove(node, { clientX: 175, clientY: 122 });
    expect(Number(rect.getAttribute("x"))).toBeCloseTo(80);
    expect(Number(rect.getAttribute("y"))).toBeCloseTo(89);
    fireEvent.pointerCancel(node);
    fireEvent.pointerMove(node, { clientX: 230, clientY: 144 });
    expect(Number(rect.getAttribute("x"))).toBeCloseTo(80);
  });

  it("preserves node clicks with minor movement and background panning", () => {
    const onNodeClick = vi.fn();
    const onSelectionClear = vi.fn();
    const { container } = renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" onNodeClick={onNodeClick} onSelectionClear={onSelectionClear} />);
    const node = screen.getByRole("button", { name: "Coordinator" });
    fireEvent.pointerDown(node, { clientX: 120, clientY: 100, button: 0 });
    fireEvent.pointerMove(node, { clientX: 121, clientY: 101 });
    fireEvent.pointerUp(node);
    fireEvent.click(node);
    expect(onNodeClick).toHaveBeenCalledWith(nodes[0]);
    expect(node.querySelector("rect")).toHaveAttribute("x", "30");
    const svg = screen.getByRole("img");
    fireEvent.pointerDown(svg, { clientX: 500, clientY: 200, button: 0 });
    fireEvent.pointerMove(svg, { clientX: 550, clientY: 225 });
    fireEvent.pointerUp(svg);
    expect(container.querySelector("g[transform]")).toHaveAttribute("transform", "translate(50 25) scale(1)");
    expect(onSelectionClear).not.toHaveBeenCalled();
    fireEvent.pointerDown(svg, { clientX: 550, clientY: 225, button: 0 });
    fireEvent.pointerUp(svg);
    expect(onSelectionClear).toHaveBeenCalledOnce();
  });

  it("ignores other pointers and non-primary buttons and stops when capture is lost", () => {
    renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" />);
    const node = screen.getByRole("button", { name: "Coordinator" });
    const rect = node.querySelector("rect")!;
    fireEvent.pointerDown(node, { clientX: 120, clientY: 100, button: 2 });
    fireEvent.pointerMove(node, { clientX: 180, clientY: 140 });
    expect(rect).toHaveAttribute("x", "30");
    fireEvent.pointerDown(node, { pointerId: 1, clientX: 120, clientY: 100, button: 0 });
    fireEvent.pointerMove(node, { pointerId: 2, clientX: 180, clientY: 140 });
    fireEvent.pointerUp(node, { pointerId: 2 });
    expect(rect).toHaveAttribute("x", "30");
    fireEvent.pointerMove(node, { pointerId: 1, clientX: 180, clientY: 140 });
    expect(rect).toHaveAttribute("x", "90");
    fireEvent.lostPointerCapture(node, { pointerId: 1 });
    fireEvent.pointerMove(node, { pointerId: 1, clientX: 240, clientY: 180 });
    expect(rect).toHaveAttribute("x", "90");
  });

  it("zooms on wheel input and restores the transform on reset", () => {
    const { container } = renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" />);
    const svg = screen.getByRole("img");
    const transform = () => container.querySelector("g[transform]")?.getAttribute("transform");
    expect(transform()).toContain("scale(1)");
    fireEvent.wheel(svg, { deltaY: -100 });
    expect(transform()).not.toContain("scale(1)");
    fireEvent.click(screen.getByRole("button", { name: "Reset canvas" }));
    expect(transform()).toContain("scale(1)");
  });
  it("shows an empty state without nodes", () => {
    renderWithI18n(<CollaborationGraphCanvas nodes={[]} edges={[]} emptyLabel="No activity" />);
    expect(screen.getByText("No activity")).toBeInTheDocument();
  });

  it("supports keyboard node selection and edge evidence", () => {
    const onNodeClick = vi.fn();
    const onEdgeClick = vi.fn();
    renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" onNodeClick={onNodeClick} onEdgeClick={onEdgeClick} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Coordinator" }), { key: "Enter" });
    expect(onNodeClick).toHaveBeenCalledWith(nodes[0]);
    fireEvent.keyDown(screen.getByRole("button", { name: "Zoom in" }), { key: "Enter" });
    expect(screen.getByRole("img")).toBeInTheDocument();
  });

  it("shows active-over-total counts for aggregate project relations", () => {
    renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={[{ ...edges[0]!, count: 5, active_count: 2 }]} emptyLabel="No activity" showAggregateCounts />);
    expect(screen.getByRole("button", {name:"Delegated · Unfinished 2 / 5"})).toBeInTheDocument();
  });

  it("expands the canvas when many nodes would otherwise overlap", () => {
    const manyNodes = Array.from({ length: 25 }, (_, index) => ({
      id: `agent-${index}`,
      type: "agent" as const,
      label: `Agent ${index}`,
      status: "working",
    }));
    renderWithI18n(<CollaborationGraphCanvas nodes={manyNodes} edges={[]} emptyLabel="No activity" />);
    const viewBox = screen.getByRole("img").getAttribute("viewBox") ?? "";
    expect(Number(viewBox.split(" ")[2])).toBeGreaterThan(760);
  });
  it("uses distinct curved paths for bidirectional relations and supports fit/reset", () => {
    const {container}=renderWithI18n(<CollaborationGraphCanvas nodes={nodes} edges={[edges[0]!, {...edges[0]!,id:"reverse",from:"agent-b",to:"agent-a"}]} emptyLabel="No activity"/>);
    const paths=[...container.querySelectorAll('path[marker-end]')].map(p=>p.getAttribute("d"));
    expect(paths).toHaveLength(2);
    expect(paths[0]).not.toBe(paths[1]);
    fireEvent.click(screen.getByRole("button",{name:"Fit canvas"}));
    fireEvent.click(screen.getByRole("button",{name:"Reset canvas"}));
  });
});
