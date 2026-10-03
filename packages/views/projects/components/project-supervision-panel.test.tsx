// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import type { Project, ProjectSupervision } from "@multica/core/types/project";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { ProjectSupervisionPanel } from "./project-supervision-panel";

vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"ws"}));
vi.mock("@multica/core/api",()=>({api:{getProjectSupervision:vi.fn(),saveProjectSupervision:vi.fn(),checkProjectSupervision:vi.fn(),listIssueStatuses:vi.fn()}}));
const project:Project={id:"p",workspace_id:"ws",title:"Project",description:null,icon:null,status:"in_progress",priority:"none",lead_type:"agent",lead_id:"lead",start_date:null,due_date:null,created_at:"",updated_at:"",issue_count:1,done_count:0,resource_count:0};
const view:ProjectSupervision={project_id:"p",workspace_id:"ws",enabled:true,revision:1,dirty_version:5,handled_version:3,checked_version:3,last_reason:"coordination_active",no_progress_count:0,last_task_id:"run",last_task_status:"running",next_check_at:null,last_checked_at:null,last_result:{},config:{auto_advance:false,max_in_flight:3,batch_size:3,scan_interval_seconds:300,stale_after_seconds:900,no_progress_limit:3,ready_statuses:["todo"]},snapshot:{counts:{ready:1,unassigned:0,executing:0,review:0,blocked:0,paused:0,stalled:0},fingerprint:"f",oldest_ready_seconds:60,actionable:1,issues:[{id:"i",identifier:"PRJ-1",title:"Ready issue",status:"todo",revision:1,assignee_type:"agent",assignee_id:"lead",category:"ready",reason:"",active_runs:0,age_seconds:60}]}};
function mount(canManage=true){
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity},mutations:{retry:false}}});
 const navigation:NavigationAdapter={pathname:"/qa/projects/p",searchParams:new URLSearchParams(),hash:"",push:vi.fn(),replace:vi.fn(),back:vi.fn(),getShareableUrl:path=>path};
 renderWithI18n(<QueryClientProvider client={client}><WorkspaceSlugProvider slug="qa"><NavigationProvider value={navigation}><ProjectSupervisionPanel project={project} canManage={canManage}/></NavigationProvider></WorkspaceSlugProvider></QueryClientProvider>,{locale:"zh-Hans"});return client;
}
describe("project supervision controls",()=>{
 beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.getProjectSupervision).mockResolvedValue(view);vi.mocked(api.listIssueStatuses).mockResolvedValue({statuses:[],categories:["unstarted","started","done","closed"],total:0});vi.mocked(api.checkProjectSupervision).mockResolvedValue(view)});
 it("shows active coordination, pending facts and issue filters with collapsed settings",async()=>{mount();expect(await screen.findByText("正在协调")).toBeVisible();expect(screen.getByText("有待处理的新变化")).toBeVisible();expect(screen.getByText("监督配置").parentElement).not.toHaveAttribute("open");fireEvent.click(screen.getByRole("button",{name:"可执行 1"}));expect(screen.getByRole("link",{name:"PRJ-1 · Ready issue"})).toHaveAttribute("href","/qa/issues/PRJ-1");fireEvent.click(screen.getByRole("button",{name:"立即检查"}));await waitFor(()=>expect(api.checkProjectSupervision).toHaveBeenCalledWith("p"))});
 it("retains the draft and original revision through refresh and failed save",async()=>{const client=mount();await screen.findByText("监督配置");fireEvent.click(screen.getByText("监督配置"));fireEvent.click(screen.getByRole("checkbox",{name:"自动推进满足条件的阶段"}));await act(async()=>{client.setQueryData(["project_supervision","ws","p"],{...view,revision:7})});vi.mocked(api.saveProjectSupervision).mockRejectedValue(new Error("conflict"));fireEvent.click(screen.getByRole("button",{name:"保存"}));expect(await screen.findByRole("alert")).toHaveTextContent("草稿已保留");expect(api.saveProjectSupervision).toHaveBeenCalledWith("p",expect.objectContaining({revision:1,config:expect.objectContaining({auto_advance:true})}));expect(screen.getByRole("checkbox",{name:"自动推进满足条件的阶段"})).toBeChecked()});
 it("keeps policy and manual checks out of member controls",async()=>{mount(false);await screen.findByText("项目监督");expect(screen.queryByText("监督配置")).not.toBeInTheDocument();expect(screen.queryByRole("button",{name:"立即检查"})).not.toBeInTheDocument()});
});
