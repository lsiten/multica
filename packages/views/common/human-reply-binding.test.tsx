import { useState, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { humanRequestKeys } from "@multica/core/human-requests";
import type { HumanRequest } from "@multica/core/types/human-request";
import { renderWithI18n } from "../test/i18n";
import { useHumanReplyBinding } from "./human-reply-binding";

vi.mock("@multica/core/api",async()=>({...await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api"),api:{listHumanRequests:vi.fn(),replyHumanRequest:vi.fn()}}));
vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"workspace"}));
vi.mock("@multica/core/realtime",()=>({useWSEvent:()=>undefined,useWSReconnect:()=>undefined}));

const request:HumanRequest={id:"request",workspace_id:"workspace",source_task_id:"source",agent_id:"agent",recipient_id:"me",issue_id:"issue",chat_session_id:null,project_id:null,revision:3,status:"pending",can_respond:true,expires_at:"2030-01-01T00:00:00Z",response:null,response_task_id:null,payload:{key:"size",kind:"choice",title:"Image dimensions",steps:[],action_label:"Submit",next:"Continue",response_mode:"chat_or_card",choices:[{id:"native",label:"Accept native size"},{id:"resize",label:"Resize"}]}};

function Harness(){
 const [text,setText]=useState("");const [outcome,setOutcome]=useState("");const binding=useHumanReplyBinding("comment","issue",text,true);
 return <><label>Message<input value={text} onChange={event=>setText(event.target.value)}/></label>{binding.preview}<button onClick={async()=>{const result=await binding.submit(text);setOutcome(result);if(result==="accepted")setText("")}}>Send</button><p>{outcome}</p></>;
}

function renderBinding(){const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});function Providers({children}:{children:ReactNode}){return <QueryClientProvider client={client}>{children}</QueryClientProvider>};renderWithI18n(<Providers><Harness/></Providers>);return client;}
beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.listHumanRequests).mockResolvedValue([request]);vi.mocked(api.replyHumanRequest).mockResolvedValue({request:{...request,status:"answered",response_task_id:"continuation",response:{revision:3,decision:"choice",answer:"native"}},reply:{channel:"comment",text:"A",reply_id:"reply",created_at:"2026-10-05T00:00:00Z"},task_id:"continuation"})});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("human reply binding",()=>{
 it("uses the visible request's validated cache while the scope list is unavailable", async()=>{
  vi.mocked(api.listHumanRequests).mockRejectedValue(new Error("list unavailable"));const client=renderBinding();client.setQueryData(humanRequestKeys.detail("workspace",request.id),request);
  await userEvent.type(screen.getByLabelText("Message"),"A");await screen.findByText(/Will confirm/);await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("accepted");expect(api.replyHumanRequest).toHaveBeenCalledTimes(1);
 });

 it("blocks an answer that expires after its visible preview without sending it as ordinary text", async()=>{
  renderBinding(); await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"A");await screen.findByText(/Will confirm/);
  vi.spyOn(Date,"now").mockReturnValue(Date.parse(request.expires_at)+1);
  await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("blocked");expect(api.replyHumanRequest).not.toHaveBeenCalled();expect(screen.getByLabelText("Message")).toHaveValue("A");
 });
 it("accepts input only after explicitly selecting its request", async()=>{
  const input={...request,payload:{...request.payload,kind:"input" as const,choices:[],input_label:"Test URL"}};
  vi.mocked(api.listHumanRequests).mockResolvedValue([input]); renderBinding();await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"https://test.example");
  expect(screen.queryByText(/Will confirm/)).not.toBeInTheDocument();
  await userEvent.selectOptions(screen.getByLabelText("Reply to request"),"request");await screen.findByText(/Will confirm/);await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("accepted");expect(api.replyHumanRequest).toHaveBeenCalledWith("request",expect.objectContaining({text:"https://test.example"}));
 });

 it("shows the exact target and sends one formal reply",async()=>{
  renderBinding();await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"A");await screen.findByText("Will confirm “Image dimensions”: Accept native size");await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("accepted");
  expect(api.replyHumanRequest).toHaveBeenCalledTimes(1);expect(api.replyHumanRequest).toHaveBeenCalledWith("request",{revision:3,text:"A",channel:"comment",scope_id:"issue"});expect(screen.getByLabelText("Message")).toHaveValue("");
 });
 it("retains text after a failed request and never falls back to an ordinary send",async()=>{
  vi.mocked(api.replyHumanRequest).mockRejectedValue(new ApiError("changed",409,"Conflict"));renderBinding();await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"1");await screen.findByText(/Will confirm/);await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("blocked");expect(screen.getByLabelText("Message")).toHaveValue("1");expect(screen.getByRole("alert")).toHaveTextContent("latest version");
 });
 it("does not reinterpret a seen answer when the request revision changes",async()=>{
  const client=renderBinding();await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"A");await screen.findByText(/Will confirm/);
  client.setQueryData(humanRequestKeys.list("workspace",{issue_id:"issue"}),[{...request,revision:4,payload:{...request.payload,choices:[{id:"resize",label:"Resize"},{id:"native",label:"Accept native size"}]}}]);
  await screen.findByText("This request changed or ended. Review its latest version.");await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("blocked");expect(api.replyHumanRequest).not.toHaveBeenCalled();
 });
 it("requires a target for multiple matching requests and offers an explicit ordinary-message mode",async()=>{
  vi.mocked(api.listHumanRequests).mockResolvedValue([request,{...request,id:"other",payload:{...request.payload,title:"Another choice"}}]);renderBinding();await screen.findByLabelText("Reply to request");await userEvent.type(screen.getByLabelText("Message"),"A");await userEvent.click(screen.getByRole("button",{name:"Send"}));await screen.findByText("blocked");expect(api.replyHumanRequest).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button",{name:"Send as ordinary message"}));await userEvent.click(screen.getByRole("button",{name:"Send"}));await waitFor(()=>expect(screen.getByText("ordinary")).toBeVisible());
 });
});
