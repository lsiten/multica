// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { Agent, Project, ProjectExecutionScopeBindings } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, AppLink, type NavigationAdapter } from "../../navigation";
import { ProjectExecutionScope } from "./project-execution-scope";
vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"ws"}));
vi.mock("@multica/core/api",()=>({api:{getProjectExecutionScopeBindings:vi.fn(),updateProjectExecutionScopeBindings:vi.fn()}}));
const project:Project={id:"project",workspace_id:"ws",title:"Project",description:null,icon:null,status:"in_progress",priority:"none",lead_type:"agent",lead_id:"lead",start_date:null,due_date:null,created_at:"",updated_at:"",issue_count:1,done_count:0,resource_count:0};
const agent:Agent={id:"lead",name:"Lead",workspace_id:"ws",runtime_id:"runtime",description:"",instructions:"",avatar_url:null,runtime_mode:"local",runtime_config:{},custom_args:[],visibility:"workspace",permission_mode:"public_to",invocation_targets:[],status:"idle",max_concurrent_tasks:1,model:"",owner_id:"owner",skills:[],created_at:"",updated_at:"",archived_at:null,archived_by:null};
const saved:ProjectExecutionScopeBindings={agent_ids:[],squad_ids:[],auto_agent_ids:["lead"],inherited_agent_ids:[],effective_agent_ids:["lead","worker"],sources:[{agent_id:"lead",source:"project_lead"},{agent_id:"worker",source:"workspace_default"}]};
const push=vi.fn();
function mount(){
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity},mutations:{retry:false}}});
 const navigation:NavigationAdapter={pathname:"/qa/projects/project",searchParams:new URLSearchParams(),hash:"",push,replace:vi.fn(),back:vi.fn(),getShareableUrl:path=>path};
 renderWithI18n(<QueryClientProvider client={client}><NavigationProvider value={navigation}><ProjectExecutionScope project={project} agents={[agent,{...agent,id:"worker",name:"Worker"}]} squads={[]}/><AppLink href="/qa/other">Leave</AppLink></NavigationProvider></QueryClientProvider>,{locale:"zh-Hans"});
 return client;
}
describe("execution scope drafts",()=>{
 beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.getProjectExecutionScopeBindings).mockResolvedValue(saved);vi.mocked(api.updateProjectExecutionScopeBindings).mockResolvedValue(saved)});
 it("starts collapsed, keeps the lead automatic, and preserves the draft through refresh and failure",async()=>{
  const client=mount();
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:/执行范围/}));
  expect(await screen.findByRole("checkbox",{name:/Lead/})).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox",{name:"Worker"}));
  expect(api.updateProjectExecutionScopeBindings).not.toHaveBeenCalled();
  await act(async()=>{client.setQueryData(["project-execution-scope-bindings","ws","project"],{...saved,sources:[]})});
  expect(screen.getByRole("checkbox",{name:"Worker"})).toBeChecked();
  vi.mocked(api.updateProjectExecutionScopeBindings).mockRejectedValue(new Error("503"));
  fireEvent.click(screen.getByRole("button",{name:"保存范围"}));
  expect(await screen.findByRole("alert")).toHaveTextContent("保存失败，草稿已保留。");
  expect(screen.getByRole("checkbox",{name:"Worker"})).toBeChecked();
 });
 it("blocks leaving with an unsaved scope and allows leaving after cancelling",async()=>{
  vi.spyOn(window,"confirm").mockReturnValue(false);
  mount();fireEvent.click(screen.getByRole("button",{name:/执行范围/}));
  fireEvent.click(await screen.findByRole("checkbox",{name:"Worker"}));
  fireEvent.click(screen.getByRole("link",{name:"Leave"}));
  expect(window.confirm).toHaveBeenCalled();
  expect(push).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button",{name:"取消"}));
  fireEvent.click(screen.getByRole("link",{name:"Leave"}));
  await waitFor(()=>expect(push).toHaveBeenCalledWith("/qa/other"));
 });
});
