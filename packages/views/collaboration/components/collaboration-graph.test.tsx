// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { CollaborationGraphEdge, CollaborationGraphNode } from "@multica/core/types";
import { CollaborationGraphCanvas } from "./collaboration-graph";

const nodes: readonly CollaborationGraphNode[] = [
  { id: "agent-a", type: "agent", label: "Coordinator", status: "working" },
  { id: "agent-b", type: "agent", label: "Builder", status: "done" },
];
const edges: readonly CollaborationGraphEdge[] = [{ id: "edge-a", from: "agent-a", to: "agent-b", type: "delegated", count: 2, active_count: 1, evidence_count: 2, last_event_at: "2026-09-30T00:00:00Z" }];

describe("CollaborationGraphCanvas", () => {
  it("shows an empty state without nodes", () => {
    render(<CollaborationGraphCanvas nodes={[]} edges={[]} emptyLabel="No activity" />);
    expect(screen.getByText("No activity")).toBeInTheDocument();
  });

  it("supports keyboard node selection and edge evidence", () => {
    const onNodeClick = vi.fn();
    const onEdgeClick = vi.fn();
    render(<CollaborationGraphCanvas nodes={nodes} edges={edges} emptyLabel="No activity" onNodeClick={onNodeClick} onEdgeClick={onEdgeClick} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Coordinator" }), { key: "Enter" });
    expect(onNodeClick).toHaveBeenCalledWith(nodes[0]);
    fireEvent.keyDown(screen.getByRole("button", { name: "放大" }), { key: "Enter" });
    expect(screen.getByRole("img")).toBeInTheDocument();
  });

  it("expands the canvas when many nodes would otherwise overlap", () => {
    const manyNodes = Array.from({ length: 25 }, (_, index) => ({
      id: `agent-${index}`,
      type: "agent" as const,
      label: `Agent ${index}`,
      status: "working",
    }));
    render(<CollaborationGraphCanvas nodes={manyNodes} edges={[]} emptyLabel="No activity" />);
    const viewBox = screen.getByRole("img").getAttribute("viewBox") ?? "";
    expect(Number(viewBox.split(" ")[2])).toBeGreaterThan(760);
  });
});
