// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const meeting = {
  id: "meeting-1",
  workspace_id: "workspace-1",
  creator_id: "user-1",
  source_type: "chat",
  source_chat_session_id: "session-1",
  source_agent_id: "agent-1",
  runtime_id: "runtime-1",
  status: "ready",
  language: "zh",
  title: "发布计划",
  timezone: "Asia/Shanghai",
  started_at: "2026-11-20T10:00:00Z",
  ended_at: "2026-11-20T10:30:00Z",
  audio_retention: "discard",
  project_id: "project-1",
  topic: "发布计划",
  transcript: "[00:00] 发言人：本周发布。",
  summary: "讨论本周发布。",
  decisions: ["周五发布"],
  action_items: ["准备发布说明"],
  classification: { source: "automatic" },
  created_at: "2026-11-20T10:00:00Z",
  updated_at: "2026-11-20T10:30:00Z",
};

afterEach(() => vi.unstubAllGlobals());

it("creates and finalizes meetings with workspace identity headers", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...meeting, status: "recording", title: "", topic: "", decisions: [], action_items: [], classification: {} })))
    .mockResolvedValueOnce(new Response(JSON.stringify(meeting)));
  vi.stubGlobal("fetch", fetch);
  const api = new ApiClient("https://example.test");

  const created = await api.createMeeting({ source_type: "chat", source_chat_session_id: "session-1", language: "zh" }, { workspaceId: "workspace-1" });
  const finished = await api.finishMeeting(created.id, { workspaceId: "workspace-1" });

  expect(created.status).toBe("recording");
  expect(finished.title).toBe("发布计划");
  expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({ "X-Workspace-ID": "workspace-1", "X-Workspace-Slug": "" });
  expect(fetch.mock.calls[1]?.[0]).toBe("https://example.test/api/meetings/meeting-1/finish");
});

it("downloads a finalized meeting file with workspace identity headers", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(new Response("# 发布计划\n", {
    headers: { "content-type": "text/markdown; charset=utf-8" },
  }));
  vi.stubGlobal("fetch", fetch);
  const api = new ApiClient("https://example.test");

  await expect(api.getMeetingFile("meeting-1", { workspaceId: "workspace-1" })).resolves.toBe("# 发布计划\n");
  expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/meetings/meeting-1/file");
  expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({ "X-Workspace-ID": "workspace-1", "X-Workspace-Slug": "" });
});

it("searches meetings and updates, archives, and deletes a record", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify([meeting])))
    .mockResolvedValueOnce(new Response(JSON.stringify(meeting)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...meeting, archived_at: "2026-11-20T11:00:00Z" })))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetch);
  const api = new ApiClient("https://example.test");

  await api.listMeetings({ search: "发布", includeArchived: true, projectId: "project-1", agentId: "agent-1" }, { workspaceId: "workspace-1" });
  await api.updateMeeting("meeting-1", { title: "新的标题", project_id: null }, { workspaceId: "workspace-1" });
  await api.archiveMeeting("meeting-1", true, { workspaceId: "workspace-1" });
  await api.deleteMeeting("meeting-1", { workspaceId: "workspace-1" });

  expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/meetings?q=%E5%8F%91%E5%B8%83&include_archived=true&project_id=project-1&agent_id=agent-1");
  expect(fetch.mock.calls[1]?.[1]?.method).toBe("PATCH");
  expect(fetch.mock.calls[2]?.[0]).toBe("https://example.test/api/meetings/meeting-1/archive");
  expect(fetch.mock.calls[3]?.[1]?.method).toBe("DELETE");
});
