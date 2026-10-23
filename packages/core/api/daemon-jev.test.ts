// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseDaemonJevModel, parseDaemonJevModels, parseBuiltinMcpServices } from "./daemon-jev";
describe("daemon model and service contracts",()=>{
 it("defaults omitted status fields and rejects malformed model registration",()=>{
  expect(parseDaemonJevModels({status:[]})).toMatchObject({models:[],status:[]});
  expect(()=>parseDaemonJevModel({id:"Example/model"})).toThrow(/registration/);
  expect(()=>parseDaemonJevModels({models:"invalid"})).toThrow(/response/);
 });
 it("preserves public tool schemas and handles missing optional diagnostics",()=>{
  expect(parseBuiltinMcpServices({services:[{name:"builtin",transport:"streamable_http",tools:[{name:"tool",inputSchema:{type:"object"}}]}]})).toMatchObject({instances:[],services:[{requires_capability:false,tools:[{inputSchema:{type:"object"}}]}]});
  expect(()=>parseBuiltinMcpServices({services:[{name:"builtin",tools:"invalid"}]})).toThrow(/response/);
 });
 it("preserves run identity and rejects malformed task binding",()=>{
  const instance = {name:"builtin",instance_id:"mcp-12",task_id:"run-a",agent_id:"agent-a",issue_id:"issue-a",chat_session_id:"chat-a",workspace_id:"ws-a",enabled:true,scope:"task",ready:true,state:"ready"};
  expect(parseBuiltinMcpServices({services:[],instances:[instance]}).instances).toEqual([instance]);
  expect(()=>parseBuiltinMcpServices({services:[],instances:[{...instance,task_id:42}]})).toThrow(/response/);
  const {task_id,agent_id,issue_id,chat_session_id,...legacyInstance} = instance;
  expect(parseBuiltinMcpServices({services:[],instances:[legacyInstance]}).instances).toEqual([legacyInstance]);
 });
});
