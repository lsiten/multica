// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
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
