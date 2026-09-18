// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { createVscreenInterventionsApi, parseVscreenInterventions } from "./vscreen-interventions";
const scope={backendIdentity:"https://fixture.invalid",accountId:"owner",workspaceId:"workspace",runtimeId:"runtime"};
const wire={id:"intervention",workspace_id:"workspace",runtime_id:"runtime",agent_id:"agent",source_task_id:"source",state:"ready_to_continue",version:1};
describe("intervention API",()=>{
 it("parses camelCase, defaults optional fields and preserves unknown states safely",()=>{
  expect(parseVscreenInterventions([{...wire,state:"future"}],scope)?.[0]).toMatchObject({workspaceId:"workspace",sourceTaskId:"source",state:"future",humanSummary:"",continuationTaskId:null});
  expect(parseVscreenInterventions([{...wire,runtime_id:"foreign"}],scope)).toBeNull();
  expect(parseVscreenInterventions([{...wire,version:"wrong"}],scope)).toBeNull();
 });
 it("continues only by explicit POST and rejects a UTF-8 summary over 2KiB",async()=>{
  const transport=vi.fn().mockResolvedValue({task_id:"child",intervention_id:"intervention"});const api=createVscreenInterventionsApi(scope,transport);const row=parseVscreenInterventions([wire],scope)![0]!;
  expect(await api.continue(row,"done",false)).toEqual({taskId:"child",interventionId:"intervention"});
  expect(transport).toHaveBeenCalledWith("/api/tasks/source/vscreen/interventions/intervention/continue",{method:"POST",body:JSON.stringify({human_summary:"done",fresh_session:false})});
  await expect(api.continue(row,"字".repeat(683),true)).rejects.toThrow("invalid_summary");expect(transport).toHaveBeenCalledTimes(1);
 });
});
