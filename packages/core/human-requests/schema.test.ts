// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../api/client";
import { HumanRequestSchema, HumanTextReplyResultSchema } from "./schema";

const id = "00000000-0000-4000-8000-000000000001";
const request = { id, workspace_id: id, source_task_id: id, agent_id: id, recipient_id: id, revision: 1, status: "pending", can_respond: true, expires_at: "2030-01-01T00:00:00Z", payload: { key: "publish", kind: "confirmation", title: "Publish to test", action_label: "Publish to test", next: "Only the test site will be updated" } };

describe("human request API boundary", () => {
  it("preserves an old response without optional fields", () => {
    expect(HumanRequestSchema.parse(request)).toMatchObject({ response: null, response_task_id: null, payload: { steps: [], choices: [] } });
  });

  it("retains unknown enums without making them actionable", () => {
    expect(HumanRequestSchema.parse({ ...request, status: "new-state", payload: { ...request.payload, kind: "new-kind" } })).toMatchObject({ status: "unknown", payload: { kind: "unknown" } });
  });

  it("does not interpret a malformed permission as permission to answer", () => {
    expect(HumanRequestSchema.parse({ ...request, can_respond: "yes" }).can_respond).toBe(false);
  });

  it.each([
    { ...request, revision: 0 },
    { ...request, expires_at: "unknown" },
    { ...request, payload: { ...request.payload, kind: "manual" } },
    { ...request, payload: { ...request.payload, kind: "input" } },
    { ...request, payload: { ...request.payload, kind: "choice", choices: [{ id: "a", label: "A" }, { id: "a", label: "Another A" }] } },
  ])("refuses malformed decisions instead of inventing controls", value => {
    expect(HumanRequestSchema.safeParse(value).success).toBe(false);
  });
});

it("keeps legacy and unsupported response modes on card controls",()=>{
 expect(HumanRequestSchema.parse(request).payload.response_mode).toBe("card_only");expect(HumanRequestSchema.parse({...request,payload:{...request.payload,response_mode:"future"}}).payload.response_mode).toBe("card_only");
});
it("rejects incomplete text reply receipts instead of claiming confirmation",()=>{
 expect(HumanTextReplyResultSchema.safeParse({request,task_id:id}).success).toBe(false);expect(HumanTextReplyResultSchema.safeParse({request:{...request,revision:0},reply:null,task_id:id}).success).toBe(false);
});

afterEach(()=>vi.unstubAllGlobals());
it("does not accept a verbal-looking HTTP success without a persisted answer",async()=>{
 vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({request,reply:null,task_id:id}),{status:200,headers:{"Content-Type":"application/json"}})));
 await expect(new ApiClient("https://api.test").replyHumanRequest(id,{revision:1,text:"A",channel:"comment",scope_id:id})).rejects.toThrow("receipt");
});
