import { useMutation } from "@tanstack/react-query";
import { api } from "../api";
import type { AppendMeetingSegmentRequest, CreateMeetingRequest, Meeting, UpdateMeetingRequest } from "../types";

export function useCreateMeeting() {
  return useMutation({
    mutationFn: ({ data, workspace }: { data?: CreateMeetingRequest; workspace?: string }) =>
      api.createMeeting(data, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useAppendMeetingSegment() {
  return useMutation({
    mutationFn: ({ meetingId, data, workspace }: { meetingId: string; data: AppendMeetingSegmentRequest; workspace?: string }) =>
      api.appendMeetingSegment(meetingId, data, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useFinishMeeting() {
  return useMutation({
    mutationFn: ({ meetingId, workspace }: { meetingId: string; workspace?: string }) =>
      api.finishMeeting(meetingId, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useCancelMeeting() {
  return useMutation({
    mutationFn: ({ meetingId, workspace }: { meetingId: string; workspace?: string }) =>
      api.cancelMeeting(meetingId, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useDownloadMeetingFile() {
  return useMutation({
    mutationFn: ({ meetingId, workspace }: { meetingId: string; workspace?: string }) =>
      api.getMeetingFile(meetingId, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useUpdateMeeting() {
  return useMutation({
    mutationFn: ({ meetingId, data, workspace }: { meetingId: string; data: UpdateMeetingRequest; workspace?: string }) =>
      api.updateMeeting(meetingId, data, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useArchiveMeeting() {
  return useMutation({
    mutationFn: ({ meetingId, archived, workspace }: { meetingId: string; archived: boolean; workspace?: string }) =>
      api.archiveMeeting(meetingId, archived, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useDeleteMeeting() {
  return useMutation({
    mutationFn: ({ meetingId, workspace }: { meetingId: string; workspace?: string }) =>
      api.deleteMeeting(meetingId, workspace ? { workspaceId: workspace } : undefined),
  });
}

export function useTranscribeRuntimeVoice() {
  return useMutation({
    mutationFn: ({ runtimeId, recording, signal, locale }: { runtimeId: string; recording: Blob; signal: AbortSignal; locale?: string }) =>
      api.transcribeRuntimeVoice(runtimeId, recording, signal, locale),
  });
}

export function useSendMeetingToAgent() {
  return useMutation({
    mutationFn: async ({ meeting, agentId, instruction }: { meeting: Meeting; agentId: string; instruction?: string }) => {
      const session = await api.createChatSession({
        agent_id: agentId,
        title: meeting.title || "会议记录",
        project_id: meeting.project_id ?? null,
      });
      const markdown = [
        `# ${meeting.title || "会议记录"}`,
        "",
        `- 主题：${meeting.topic || "未分类"}`,
        `- 开始时间：${meeting.started_at}`,
        `- 时区：${meeting.timezone}`,
        meeting.project_id ? `- 项目：${meeting.project_id}` : "- 项目：未关联",
        "",
        "## 摘要",
        meeting.summary || "暂无摘要",
        "",
        "## 决策",
        ...(meeting.decisions.length ? meeting.decisions.map((item) => `- ${item}`) : ["- 暂无"]),
        "",
        "## 行动项",
        ...(meeting.action_items.length ? meeting.action_items.map((item) => `- ${item}`) : ["- 暂无"]),
        "",
        "## 原始转写",
        meeting.transcript || "暂无转写",
        "",
      ].join("\n");
      const safeTitle = (meeting.title || "meeting-record").replace(/[\\/:*?"<>|]/g, "_").slice(0, 80);
      const attachment = await api.uploadFile(
        new File([markdown], `${safeTitle || "meeting-record"}.md`, { type: "text/markdown" }),
        { chatSessionId: session.id },
      );
      const body = [
        instruction?.trim() || "请阅读附件中的会议记录，提取需要跟进的事项。",
        "",
        `会议标题：${meeting.title || "会议记录"}`,
        `主题：${meeting.topic || "未分类"}`,
        `摘要：${meeting.summary || ""}`,
        "",
        "决策：",
        ...meeting.decisions.map((item) => `- ${item}`),
        "",
        "行动项：",
        ...meeting.action_items.map((item) => `- ${item}`),
        "",
      ].join("\n");
      await api.sendChatMessage(session.id, body, [attachment.id]);
      return session;
    },
  });
}
