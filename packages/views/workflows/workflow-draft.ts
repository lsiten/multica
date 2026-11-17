import type { IssueWorkflow, IssueWorkflowStep } from "@multica/core/types";
import { workflowMiddleKeys } from "@multica/core/issue-workflows";

/** A new workflow starts from the common delivery loop instead of nothing. */
export const NEW_WORKFLOW_MIDDLE = ["in_progress", "in_review"];

/** A middle column of the draft: a library status, or one to create on save. */
export type DraftColumn = { id: string; status_key: string } | { id: string; new_name: string; category?: "unstarted" | "started" };

/** What entering a column does, keyed in the draft by column id. */
export type Handoff = Pick<IssueWorkflowStep, "handler" | "instructions" | "next_status_key" | "back_status_key">;

export const NO_HANDOFF: Handoff = { handler: { type: "none" }, instructions: "" };

export interface Draft {
  name: string;
  description: string;
  backlog: boolean;
  blocked: boolean;
  middle: DraftColumn[];
  handoffs: Record<string, Handoff>;
}

/**
 * The key a column goes by in the editor: its status, or — for a status the
 * save creates — its draft id, so its handoff can be edited before the save.
 */
export function columnKey(column: DraftColumn): string {
  return "status_key" in column ? column.status_key : column.id;
}

export function hasHandoff(h: Handoff | undefined): h is Handoff {
  return !!h && (h.handler.type !== "none" || !!h.next_status_key || !!h.back_status_key);
}

export function draftFrom(workflow: Pick<IssueWorkflow, "name" | "description" | "steps">): Draft {
  const keys = workflow.steps.map((s) => s.status_key);
  const handoffs: Record<string, Handoff> = {};
  for (const s of workflow.steps) {
    const h = { handler: s.handler, instructions: s.instructions, next_status_key: s.next_status_key, back_status_key: s.back_status_key };
    if (hasHandoff(h)) handoffs[s.status_key] = h;
  }
  return {
    name: workflow.name,
    description: workflow.description,
    backlog: keys.includes("backlog"),
    blocked: keys.includes("blocked"),
    middle: workflowMiddleKeys(workflow).map((key) => ({ id: key, status_key: key })),
    handoffs,
  };
}

/** Every column of the draft in board order. */
export function draftColumns(draft: Draft): DraftColumn[] {
  const fixed = (key: string): DraftColumn => ({ id: key, status_key: key });
  return [
    ...(draft.backlog ? [fixed("backlog")] : []),
    fixed("todo"),
    ...draft.middle,
    ...(draft.blocked ? [fixed("blocked")] : []),
    fixed("done"),
    fixed("cancelled"),
  ];
}

export function sameDraft(a: Draft, b: Draft): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}
