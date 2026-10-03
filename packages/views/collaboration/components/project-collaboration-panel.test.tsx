// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { ProjectCollaborationGraphResponse, ProjectCollaborationEvidenceResponse } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { ProjectCollaborationPanel } from "./project-collaboration-panel";
vi.mock("@multica/core/hooks",()=>({useWorkspaceId:()=>"ws"}));
vi.mock("@multica/core/api",()=>({api:{getProjectCollaborationGraph:vi.fn(),getProjectCollaborationEvidence:vi.fn(),getAttachmentBlob:vi.fn()}}));
vi.mock("@multica/core/paths",()=>({useWorkspacePaths:()=>({issueDetail:(id:string)=>"/qa/issues/"+id})}));
const graph:ProjectCollaborationGraphResponse={project_id:"project",nodes:[{id:"agent:a",type:"agent",label:"甲",data:{task_count:1,active_count:1}},{id:"agent:b",type:"agent",label:"乙",data:{task_count:1,active_count:1}}],edges:[{id:"edge",from:"agent:a",to:"agent:b",type:"delegated",count:1,active_count:1,evidence_count:2,last_event_at:null}],summary:{task_count:1,active_count:1,agent_count:2,coverage:"complete"},as_of:"",limit:100,offset:0,has_more:false};
const evidence:ProjectCollaborationEvidenceResponse={project_id:"project",evidence:[{task_id:"run",source_task_id:"parent-run",agent_id:"b",agent_name:"乙",source_agent_id:"a",source_agent_name:"甲",relation_type:"delegated",status:"failed",task_active:true,issue_id:"child",issue_key:"QA-2",issue_title:"审查子任务",issue_status:"in_review",squad_id:null,trigger_comment_id:"comment",created_at:"2026-10-02T00:00:00Z",started_at:null,completed_at:null,event_count:1,artifacts:[{id:"file",filename:"review.txt"}]}],issues:[{id:"parent",key:"QA-1",title:"父任务",status:"done",parent_issue_id:null,context_only:true},{id:"child",key:"QA-2",title:"审查子任务",status:"in_review",parent_issue_id:"parent",context_only:false}],total:1,limit:100,offset:0,has_more:false,as_of:"",truncated:false};
const navigation:NavigationAdapter={pathname:"/qa/projects/project",searchParams:new URLSearchParams(),hash:"",push:vi.fn(),replace:vi.fn(),back:vi.fn(),getShareableUrl:path=>path};
function mount(){const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});const view=renderWithI18n(<QueryClientProvider client={client}><NavigationProvider value={navigation}><ProjectCollaborationPanel projectId="project" agents={[]} squads={[]}/></NavigationProvider></QueryClientProvider>,{locale:"zh-Hans"});return {client,...view}}
describe("project collaboration interactions",()=>{
 beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.getProjectCollaborationGraph).mockResolvedValue(graph);vi.mocked(api.getProjectCollaborationEvidence).mockResolvedValue(evidence)});
 it("combines unfinished task activity with run status and title search",async()=>{
  mount();await screen.findByRole("button",{name:"甲"});
  fireEvent.change(screen.getByLabelText("运行状态过滤"),{target:{value:"failed"}});
  await waitFor(()=>expect(api.getProjectCollaborationGraph).toHaveBeenLastCalledWith("project",expect.objectContaining({activity:"active",status:"failed"})));
  fireEvent.change(screen.getByLabelText("任务"),{target:{value:"审查"}});
  await waitFor(()=>expect(api.getProjectCollaborationGraph).toHaveBeenLastCalledWith("project",expect.objectContaining({activity:"active",task_query:"审查"})));
 });
 it("opens a real task hierarchy drawer with context parent and artifact",async()=>{
  mount();fireEvent.click(await screen.findByRole("button",{name:"委派 · 未完成 1 / 共 1"}));
  await screen.findByRole("dialog");
  expect(await screen.findByText("QA-1 · 父任务")).toBeInTheDocument();
  expect(screen.getByText("上下文任务")).toBeInTheDocument();
  expect(screen.getByText("QA-2 · 审查子任务")).toBeInTheDocument();
  fireEvent.click(screen.getByText(/乙 · 失败/));
  expect(screen.getByRole("button",{name:"review.txt"})).toBeInTheDocument();
  expect(screen.getByRole("link",{name:"查看触发评论"})).toHaveAttribute("href","/qa/issues/QA-2#comment-comment");
  fireEvent.click(screen.getByRole("button",{name:"Close"}));
  await waitFor(()=>expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 });
 it("hides independent agents by default and can reveal them",async()=>{
  vi.mocked(api.getProjectCollaborationGraph).mockResolvedValue({...graph,edges:[]});
  mount();await screen.findByText("暂无项目运行协作记录。");
  expect(screen.queryByRole("button",{name:"甲"})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("checkbox",{name:"显示独立智能体"}));
  expect(await screen.findByRole("button",{name:"甲"})).toBeInTheDocument();
 });
 it("clears a selected relation when it disappears on refresh",async()=>{
  const {client}=mount();
  fireEvent.click(await screen.findByRole("button",{name:"委派 · 未完成 1 / 共 1"}));
  await screen.findByRole("dialog");
  await act(async()=>client.setQueryData(["collaboration-graph","project","ws","project",{activity:"active"}],{...graph,edges:[]}));
  await waitFor(()=>expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 });
});
