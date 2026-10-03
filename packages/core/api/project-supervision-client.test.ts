// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const view={project_id:"p",workspace_id:"ws",enabled:true,revision:1,dirty_version:9,handled_version:7,config:{},snapshot:{counts:{ready:2,unassigned:1,executing:3,review:0,blocked:0,paused:0,stalled:0},issues:[],fingerprint:"fingerprint",oldest_ready_seconds:120,actionable:3}};
afterEach(()=>vi.unstubAllGlobals());
function response(body:unknown){vi.stubGlobal("fetch",vi.fn().mockImplementation(async()=>new Response(JSON.stringify(body),{headers:{"Content-Type":"application/json"}})))}
describe("project supervision boundary",()=>{
 it("supplies additive defaults and preserves pending versions",async()=>{response(view);const parsed=await new ApiClient("https://api.example.test").getProjectSupervision("p");expect(parsed.config.max_in_flight).toBe(3);expect(parsed.checked_version).toBe(0);expect(parsed.dirty_version).toBe(9);expect(parsed.last_result).toEqual({})});
 it.each([{...view,snapshot:{counts:"wrong"}},{...view,project_id:"other"},{...view,enabled:"yes"}])("rejects malformed or foreign snapshots",async body=>{response(body);await expect(new ApiClient("https://api.example.test").getProjectSupervision("p")).rejects.toThrow("Invalid project supervision response")});
 it("does not confirm an unchanged save revision",async()=>{response(view);const client=new ApiClient("https://api.example.test");const parsed=await client.getProjectSupervision("p");await expect(client.saveProjectSupervision("p",{enabled:true,revision:1,config:parsed.config})).rejects.toThrow("save was not confirmed")});
});
