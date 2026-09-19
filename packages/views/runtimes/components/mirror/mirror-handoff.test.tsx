// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../../test/i18n";
import { MirrorHandoff } from "./mirror-handoff";
import { MirrorPlatformProvider } from "./mirror-platform";
import type { VscreenLocalOperation, VscreenLocalResult } from "@multica/core/types";
const mocks=vi.hoisted(()=>({list:vi.fn(),resume:vi.fn(),cancel:vi.fn()}));
vi.mock("@multica/core/api",()=>({getApi:()=>({vscreen:()=>({interventions:{list:mocks.list,continue:mocks.resume,cancel:mocks.cancel}})}),vscreenErrorReason:()=>null}));
vi.mock("../../../common/task-transcript",()=>({TranscriptButton:()=>null}));
vi.mock("@multica/core/runtimes",()=>({vscreenKeys:{all:()=>["scoped","workspace","runtime"]},vscreenInterventionsOptions:()=>({queryKey:["scoped","workspace","runtime","interventions"],queryFn:mocks.list,retry:false})}));
const scope={backendIdentity:"https://fixture.invalid",accountId:"owner",workspaceId:"workspace",runtimeId:"runtime"};
const row={id:"intervention",workspaceId:"workspace",runtimeId:"runtime",agentId:"agent",sourceTaskId:"source",state:"awaiting_takeover",returnReceiptId:"receipt",continuationTaskId:null,version:1};
const catalog=[{resource:{backendIdentity:scope.backendIdentity,workspaceId:scope.workspaceId,runtimeId:scope.runtimeId,uid:501},source:{kind:"physical" as const,sourceId:"display:physical"},nativeEpoch:"native",generation:"display",primary:true,name:"Built-in display",width:1600,height:900,logicalWidth:1600,logicalHeight:900,scale:1,x:0,y:0,geometryRevision:1,displayId:1}];
function render(localControl?: (_scope:typeof scope,op:VscreenLocalOperation)=>Promise<VscreenLocalResult>){
 const cache=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});const request=vi.fn();
 renderWithI18n(<QueryClientProvider client={cache}><MirrorPlatformProvider value={{localControl}}><MirrorHandoff scope={scope} owner online canRequest catalog={catalog} onRequest={request}/></MirrorPlatformProvider></QueryClientProvider>);
 return {request,cache};
}
beforeEach(()=>{vi.clearAllMocks();mocks.list.mockResolvedValue([row]);mocks.resume.mockResolvedValue({taskId:"child",interventionId:row.id});});
describe("mirror handoff",()=>{
 it("never transfers on mount or source viewing and sends only explicit local action",async()=>{
  const local=vi.fn(async(_scope: typeof scope,_op: VscreenLocalOperation)=>({ok:true,local:true}));render(local);
  const select=await screen.findByLabelText("Move to display");fireEvent.change(select,{target:{value:"display:physical"}});
  expect(local.mock.calls.filter(([,op])=>op.action!=="status")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button",{name:"Move app here"}));
  await waitFor(()=>expect(local).toHaveBeenCalledWith(scope,{action:"takeover",interventionId:"intervention",destinationSourceId:"display:physical"}));
 });
 it("remote UI can request host takeover but cannot move a physical window",async()=>{
  mocks.list.mockResolvedValue([]);const {request}=render();fireEvent.click(await screen.findByRole("button",{name:"Request takeover"}));expect(request).toHaveBeenCalledOnce();expect(screen.queryByRole("button",{name:"Move app here"})).toBeNull();
 });
 it("report_pending blocks repeated transfer and retains state until explicit refresh",async()=>{
  const local=vi.fn(async(_scope: typeof scope,op: VscreenLocalOperation)=>op.action==="status"?{ok:true,local:true}:{ok:false,local:true,reason:"report_pending"});render(local);
  fireEvent.change(await screen.findByLabelText("Move to display"),{target:{value:"display:physical"}});fireEvent.click(screen.getByRole("button",{name:"Move app here"}));
  await screen.findByText(/Waiting for the server to confirm/);expect(screen.getByRole("button",{name:"Move app here"})).toBeDisabled();expect(local.mock.calls.filter(([,op])=>op.action==="takeover")).toHaveLength(1);
 });
 it("continue and fresh-session retry require separate explicit clicks",async()=>{
  mocks.list.mockResolvedValue([{...row,state:"ready_to_continue"}]);mocks.resume.mockRejectedValueOnce(new Error("resume_unavailable"));render();
  fireEvent.click(await screen.findByRole("button",{name:"Continue run"}));await screen.findByRole("button",{name:"Start a fresh session"});expect(mocks.resume).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button",{name:"Start a fresh session"}));await waitFor(()=>expect(mocks.resume).toHaveBeenLastCalledWith(expect.objectContaining({id:row.id}),"",true));
 });
 it("shows explicit selection for an empty local intervention, not the physical transfer control",async()=>{
  const local=vi.fn(async()=>({ok:true,local:true,interventionId:row.id,selectionRequired:true}));render(local);
  await screen.findByRole("button",{name:"List local windows"});expect(screen.queryByRole("button",{name:"Move app here"})).toBeNull();expect(local).toHaveBeenCalledTimes(1);
 });
 it("never exposes local window selection to the remote surface",async()=>{
  render();await screen.findByText("Waiting for local takeover");expect(screen.queryByRole("button",{name:"List local windows"})).toBeNull();
 });

});
