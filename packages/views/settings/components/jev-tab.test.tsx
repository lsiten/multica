// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";

const api=vi.hoisted(()=>({get:vi.fn(),update:vi.fn()}));
const daemon=vi.hoisted(()=>({getJevModels:vi.fn(),registerJevModel:vi.fn(),installJevModel:vi.fn(),cancelJevModelInstall:vi.fn()}));
vi.mock("@multica/core/api",async importOriginal=>({...await importOriginal<typeof import("@multica/core/api")>(),api:{getWorkspaceJevConfig:api.get,updateWorkspaceJevConfig:api.update}}));
vi.mock("@multica/core/paths",()=>({useCurrentWorkspace:()=>({id:"ws-1",name:"Acme"})}));
vi.mock("@multica/core/permissions",()=>({useCurrentMember:()=>({role:"owner"})}));
import { JevTab } from "./jev-tab";
const revision="533964dae8be954c5b5e19fa4948e48408094c1e";
const custom={id:"Example/decider",revision:"a".repeat(40),download_bytes:100,devices:["auto","cpu"]};
const initial={workspace_id:"ws-1",revision:3,config:{source:"agent_context",timeout_seconds:30,revision:3}};
function mount(){return renderWithI18n(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}})}><JevTab/></QueryClientProvider>)}
beforeEach(()=>{
 cleanup();vi.clearAllMocks();
 api.get.mockResolvedValue(initial);api.update.mockImplementation(async(_ws,request)=>({...initial,revision:4,config:{...request.config,revision:4}}));
 daemon.getJevModels.mockResolvedValue({models:[{id:"Mapika/decider-2b",revision}],status:[]});
 daemon.registerJevModel.mockResolvedValue(custom);
 daemon.installJevModel.mockResolvedValue({accepted:true});daemon.cancelJevModelInstall.mockResolvedValue({cancelled:true});
 Object.defineProperty(window,"daemonAPI",{configurable:true,value:daemon});
});
describe("JevTab",()=>{
 it("keeps source changes explicit and downloads only after confirmation",async()=>{
  const user=userEvent.setup();mount();
  expect(await screen.findByRole("button",{name:"Save Jev configuration"})).toBeDisabled();
  await user.selectOptions(screen.getByRole("combobox",{name:"Model source"}),"local");
  expect(daemon.installJevModel).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button",{name:"Confirm and download"}));
  await waitFor(()=>expect(daemon.installJevModel).toHaveBeenCalledWith("Mapika/decider-2b",revision));
  await user.click(screen.getByRole("button",{name:"Save Jev configuration"}));
  await waitFor(()=>expect(api.update).toHaveBeenCalledWith("ws-1",expect.objectContaining({revision:3})));
 });
 it("checks a custom repository, selects the pinned result and saves its device",async()=>{
  const user=userEvent.setup();mount();
  await user.selectOptions(await screen.findByRole("combobox",{name:"Model source"}),"local");
  await user.click(screen.getByText("Add a Hugging Face model"));
  await user.type(screen.getByRole("textbox",{name:"Repository ID"}),custom.id);
  await user.click(screen.getByRole("button",{name:"Check and add"}));
  await waitFor(()=>expect(screen.getByRole("combobox",{name:"Select local model"})).toHaveValue(custom.id+"@"+custom.revision));
  expect(daemon.registerJevModel).toHaveBeenCalledWith(custom.id,"main");expect(daemon.installJevModel).not.toHaveBeenCalled();
  await user.selectOptions(screen.getByRole("combobox",{name:"Device"}),"cpu");
  await user.click(screen.getByRole("button",{name:"Save Jev configuration"}));
  await waitFor(()=>expect(api.update).toHaveBeenCalledWith("ws-1",expect.objectContaining({config:expect.objectContaining({model_id:custom.id,model_revision:custom.revision,device:"cpu"})})));
 });
 it("retains an invalid repository draft and shows the daemon error",async()=>{
  daemon.registerJevModel.mockRejectedValue(new Error("model must include Decider configuration"));
  const user=userEvent.setup();mount();await user.selectOptions(await screen.findByRole("combobox",{name:"Model source"}),"local");
  await user.click(screen.getByText("Add a Hugging Face model"));await user.type(screen.getByRole("textbox",{name:"Repository ID"}),"Example/generic");
  await user.click(screen.getByRole("button",{name:"Check and add"}));
  expect(await screen.findByRole("alert")).toHaveTextContent("Decider configuration");
  expect(screen.getByRole("textbox",{name:"Repository ID"})).toHaveValue("Example/generic");
 });
 it("cancels the selected model revision",async()=>{
  daemon.getJevModels.mockResolvedValue({models:[],status:[{model_id:"Mapika/decider-2b",revision,state:"downloading",downloaded_bytes:10,total_bytes:100}]});
  const user=userEvent.setup();mount();await user.selectOptions(await screen.findByRole("combobox",{name:"Model source"}),"local");
  await user.click(await screen.findByRole("button",{name:"Cancel download"}));
  await waitFor(()=>expect(daemon.cancelJevModelInstall).toHaveBeenCalledWith("Mapika/decider-2b",revision));
 });
 it("surfaces workspace configuration failures",async()=>{
  api.get.mockRejectedValue(new Error("network failed"));mount();expect(await screen.findByRole("alert")).toHaveTextContent("Failed to load Jev configuration");
 });
 it("keeps downloaded weights installed after the worker stops",async()=>{
  daemon.getJevModels.mockResolvedValue({models:[],status:[{model_id:"Mapika/decider-2b",revision,state:"stopped",installed:true}]});
  const user=userEvent.setup();mount();await user.selectOptions(await screen.findByRole("combobox",{name:"Model source"}),"local");
  expect(await screen.findByText("Service stopped")).toBeInTheDocument();
  expect(screen.getByRole("button",{name:"Downloaded"})).toBeDisabled();
  expect(screen.queryByRole("button",{name:"Confirm and download"})).not.toBeInTheDocument();
 });
 it("shows the web boundary without downloading weights",async()=>{
  delete (window as unknown as {daemonAPI?:unknown}).daemonAPI;
  const user=userEvent.setup();mount();await user.selectOptions(await screen.findByRole("combobox",{name:"Model source"}),"local");
  expect(screen.getByText("Web mode cannot download host models. Open the desktop app.")).toBeInTheDocument();
  expect(screen.getByRole("button",{name:"Confirm and download"})).toBeDisabled();
 });
});
