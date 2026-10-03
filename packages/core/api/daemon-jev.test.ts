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
});
