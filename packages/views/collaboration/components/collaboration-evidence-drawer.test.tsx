// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { ProjectCollaborationEvidence, ProjectCollaborationEvidenceResponse } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider } from "../../navigation";
import { CollaborationEvidenceDrawer } from "./collaboration-evidence-drawer";

vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"ws"}));
vi.mock("@multica/core/api",()=>({api:{getProjectCollaborationEvidence:vi.fn(),getAttachmentBlob:vi.fn()}}));
vi.mock("@multica/core/paths",()=>({useWorkspacePaths:()=>({issueDetail:(id:string)=>"/qa/issues/"+id})}));
const run:ProjectCollaborationEvidence={task_id:"run",source_task_id:"source",agent_id:"b",agent_name:"乙",source_agent_id:"a",relation_type:"delegated",status:"completed",issue_id:"child",issue_title:"子任务",issue_key:"QA-2",squad_id:null,trigger_comment_id:null,created_at:"2020-01-01T00:00:00Z",started_at:null,completed_at:null,event_count:0};
const response:ProjectCollaborationEvidenceResponse={project_id:"project",evidence:[run],total:2,limit:1,offset:0,has_more:true,next_cursor:"next",as_of:"2026-10-02T00:00:00Z",truncated:false};
function mount(){return renderWithI18n(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><NavigationProvider value={{pathname:"/qa/projects/project",searchParams:new URLSearchParams(),hash:"",push:vi.fn(),replace:vi.fn(),back:vi.fn(),getShareableUrl:path=>path}}><CollaborationEvidenceDrawer projectId="project" filters={{activity:"active",agent_id:"a"}} selection={{node:{id:"agent:b",type:"agent",label:"乙"}}} onClose={vi.fn()}/></NavigationProvider></QueryClientProvider>,{locale:"zh-Hans"})}

describe("collaboration evidence",()=>{
 beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.getProjectCollaborationEvidence).mockResolvedValue(response)});
 it("intersects node selection with the main filters and uses server sort and cursor",async()=>{
  mount();await screen.findByText("QA-2 · 子任务");
  expect(api.getProjectCollaborationEvidence).toHaveBeenCalledWith("project",expect.objectContaining({agent_id:"a",node_agent_id:"b",sort:"newest",cursor_mode:true}));
  fireEvent.change(screen.getByLabelText("排序"),{target:{value:"oldest"}});
  await waitFor(()=>expect(api.getProjectCollaborationEvidence).toHaveBeenLastCalledWith("project",expect.objectContaining({agent_id:"a",node_agent_id:"b",sort:"oldest"})));
  fireEvent.click(await screen.findByRole("button",{name:"加载更多"}));
  await waitFor(()=>expect(api.getProjectCollaborationEvidence).toHaveBeenLastCalledWith("project",expect.objectContaining({cursor:"next",snapshot_at:response.as_of,sort:"oldest"})));
  fireEvent.change(screen.getByLabelText("运行状态过滤"),{target:{value:"failed"}});
  await waitFor(()=>expect(api.getProjectCollaborationEvidence).toHaveBeenLastCalledWith("project",expect.objectContaining({status:"failed",agent_id:"a",node_agent_id:"b"})));
 });
 it("opens the authorized source run with a task link instead of leaving a UUID",async()=>{
  vi.mocked(api.getProjectCollaborationEvidence).mockImplementation(async(_project,params)=>params?.run_id?{...response,has_more:false,evidence:[{...run,task_id:"source",source_task_id:null,issue_id:"parent",issue_title:"来源任务",issue_key:"QA-1",agent_name:"甲"}]}:response);
  mount();await screen.findByText("QA-2 · 子任务");
  fireEvent.click(screen.getByText(/乙 · 已完成/));
  fireEvent.click(screen.getByRole("button",{name:"查看来源运行"}));
  expect(await screen.findByRole("link",{name:"QA-1 · 来源任务"})).toHaveAttribute("href","/qa/issues/QA-1");
  expect(api.getProjectCollaborationEvidence).toHaveBeenLastCalledWith("project",{activity:"all",run_id:"source",limit:1});
 });
});
