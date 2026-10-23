// @vitest-environment node
import { expect, it } from "vitest";
import { pagedReviewRequestSchema, parsePagedReviewResponse } from "./local-review-pages";

const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main", action: "file", version_id: id, file_path: "file.txt", offset: 0, limit: 20 } as const;
const response = { version_id: id, path: "file.txt", preview: "text", page: { lines: [{ text: "+change", kind: "add", new_line: 1 }], next_line: 1, has_more: true } };

it("accepts one project directory scope and rejects ambiguous scopes or writes", () => {
  const directory = { workspace_id: "ws", project_resource_id: "resource", path: "/project", action: "workspace_files" } as const;
  expect(pagedReviewRequestSchema.parse(directory)).toMatchObject({ task_id: "", project_resource_id: "resource" });
  expect(pagedReviewRequestSchema.safeParse({ ...directory, task_id: "task" }).success).toBe(false);
  expect(pagedReviewRequestSchema.safeParse({ ...request, project_resource_id: "resource", task_id: "", action: "stage" }).success).toBe(false);
});

it("parses bounded patch pages", () => {
  expect(parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), response)).toMatchObject(response);
});
it("rejects a response for another version", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, version_id: "b".repeat(64) })).toThrow();
});
it("rejects a file outside the selected identity", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, path: "other.txt" })).toThrow();
});
it("rejects non-progressing pagination", () => {
  expect(() => parsePagedReviewResponse(pagedReviewRequestSchema.parse(request), { ...response, page: { ...response.page, next_line: 0 } })).toThrow();
});
it("rejects a decision without matching version and operation identity", () => {
  expect(() => pagedReviewRequestSchema.parse({ ...request, action: "approve" })).toThrow();
});

it("validates UTF8 content ranges without splitting code points", () => {
  const input = pagedReviewRequestSchema.parse({ ...request, action: "content", limit: 4 });
  const page = { version_id: id, path: "file.txt", side: "new", content: { text: "中文", encoding: "utf8", offset: 0, next_offset: 6, size: 9, has_more: true } };
  expect(parsePagedReviewResponse(input, page)).toEqual(page);
  expect(() => parsePagedReviewResponse(input, { ...page, side: "old" })).toThrow();
  expect(() => parsePagedReviewResponse(input, { ...page, content: { ...page.content, next_offset: 5 } })).toThrow();
});

it("validates shallow directory pagination and preserves older response compatibility",()=>{
 const input=pagedReviewRequestSchema.parse({task_id:"task",workspace_id:"ws",path:"/repo",action:"workspace_files",recursive:false,offset:500,limit:500});
 const page={path:"/repo",recursive:false,entries:[{path:"00-项目管理",size:0,directory:true}],next_offset:501,has_more:false};
 expect(parsePagedReviewResponse(input,page)).toMatchObject(page);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:500,has_more:true})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:undefined})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,next_offset:499})).toThrow(/pagination/);
 expect(()=>parsePagedReviewResponse(input,{...page,entries:[],has_more:true})).toThrow(/pagination/);
 expect(parsePagedReviewResponse(input,{path:"/repo",entries:[]})).toMatchObject({truncated:false});
});
