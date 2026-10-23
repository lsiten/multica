// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { readWorkspaceDirectory } from "./workspace-directory-query";
const read = vi.hoisted(()=>vi.fn());
vi.mock("../../platform/local-review-pages",()=>({requestReviewPage:read}));
beforeEach(()=>read.mockReset());
const input={workspace_id:"ws",project_resource_id:"resource",path:"/project",runtime_id:"runtime"};
it("reads all immediate-child pages without walking any descendant",async()=>{
 read.mockResolvedValueOnce({path:"/project",recursive:false,entries:[{path:".audit",directory:true,size:0}],next_offset:500,has_more:true,truncated:false});
 read.mockResolvedValueOnce({path:"/project",recursive:false,entries:[{path:"00-项目管理",directory:true,size:0}],next_offset:501,has_more:false,truncated:false});
 const result=await readWorkspaceDirectory(input,".",new AbortController().signal);
 expect(result.entries.map(entry=>entry.path)).toEqual([".audit","00-项目管理"]);
 expect(read.mock.calls.map(([request])=>({relative:request.relative_path,offset:request.offset,recursive:request.recursive}))).toEqual([{relative:".",offset:0,recursive:false},{relative:".",offset:500,recursive:false}]);
});
it("does not present a capped recursive result as a complete directory",async()=>{
 read.mockResolvedValueOnce({path:"/project",entries:[],truncated:true});
 await expect(readWorkspaceDirectory(input,".",new AbortController().signal)).rejects.toThrow(/Update/);
});

it("accepts an empty final page when a directory changes between reads",async()=>{
 read.mockResolvedValueOnce({path:"/project",recursive:false,entries:[{path:"docs",directory:true,size:0}],next_offset:500,has_more:true,truncated:false});
 read.mockResolvedValueOnce({path:"/project",recursive:false,entries:[],next_offset:500,has_more:false,truncated:false});
 expect((await readWorkspaceDirectory(input,".",new AbortController().signal)).entries).toHaveLength(1);
});
