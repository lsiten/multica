import type { AgentTask } from "../types/agent";
import { AgentTaskListSchema } from "./schemas";
import { z } from "zod";
import type { VscreenScope } from "../types/vscreen";
import { parseWithFallback } from "./schema";

const interventionSchema = z.object({
  id: z.string().min(1), workspace_id: z.string().min(1), runtime_id: z.string().min(1),
  agent_id: z.string().min(1), source_task_id: z.string().min(1), state: z.string(), reason: z.string().default(""),
  human_summary: z.string().default(""), return_receipt_id: z.string().default(""),
  continuation_task_id: z.string().nullable().optional(), version: z.number().int().positive(),
}).transform((row) => ({
  id: row.id, workspaceId: row.workspace_id, runtimeId: row.runtime_id,
  agentId: row.agent_id, sourceTaskId: row.source_task_id, state: row.state, reason: row.reason,
  humanSummary: row.human_summary, returnReceiptId: row.return_receipt_id,
  continuationTaskId: row.continuation_task_id ?? null, version: row.version,
}));
export type VscreenIntervention = z.infer<typeof interventionSchema>;
export function parseVscreenInterventions(raw: unknown, scope: VscreenScope): VscreenIntervention[] | null {
  const rows = parseWithFallback<VscreenIntervention[] | null>(raw, z.array(interventionSchema), null, { endpoint: "vscreen interventions" });
  return rows?.every((row) => row.workspaceId === scope.workspaceId && row.runtimeId === scope.runtimeId) ? rows : null;
}
export function createVscreenInterventionsApi(scope: VscreenScope, request: (path: string, init?: RequestInit) => Promise<unknown>) {
  const path = (row: VscreenIntervention) => {
    if (row.workspaceId !== scope.workspaceId || row.runtimeId !== scope.runtimeId) throw new Error("Intervention scope changed");
    return `/api/tasks/${encodeURIComponent(row.sourceTaskId)}/vscreen/interventions/${encodeURIComponent(row.id)}`;
  };
  return {
    async run(row: VscreenIntervention, taskId: string) {
      path(row);
      if (taskId !== row.sourceTaskId && taskId !== row.continuationTaskId) throw new Error("Intervention scope changed");
      const tasks = parseWithFallback<AgentTask[]>(await request(`/api/agents/${encodeURIComponent(row.agentId)}/tasks`), AgentTaskListSchema, [], { endpoint: "intervention run" });
      return tasks.find((task) => task.id === taskId && task.agent_id === row.agentId && task.runtime_id === scope.runtimeId) ?? null;
    },
    async list(signal?: AbortSignal) { return parseVscreenInterventions(await request("/vscreen/interventions", { signal }), scope); },
    async continue(row: VscreenIntervention, summary: string, freshSession: boolean) {
      if (new TextEncoder().encode(summary).length > 2048) throw new Error("invalid_summary");
      const raw = await request(`${path(row)}/continue`, { method: "POST", body: JSON.stringify({ human_summary: summary, fresh_session: freshSession }) });
      const result = parseWithFallback<{ taskId: string; interventionId: string } | null>(raw,
        z.object({ task_id: z.string().min(1), intervention_id: z.literal(row.id) }).transform((value) => ({ taskId: value.task_id, interventionId: value.intervention_id })), null, { endpoint: "continue vscreen intervention" });
      if (!result) throw new Error("intervention_failed");
      return result;
    },
    async get(row: VscreenIntervention, signal?: AbortSignal) {
      const raw = await request(path(row), { signal });
      const rows = parseVscreenInterventions([raw], scope);
      return rows?.[0]?.id === row.id ? rows[0] : null;
    },
    async cancel(row: VscreenIntervention) { await request(`${path(row)}/cancel`, { method: "POST" }); },
  };
}
