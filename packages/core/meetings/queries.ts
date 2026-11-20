import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const meetingKeys = {
  all: (workspaceId: string) => ["meetings", workspaceId] as const,
  list: (workspaceId: string) => [...meetingKeys.all(workspaceId), "list"] as const,
  detail: (workspaceId: string, meetingId: string) => [...meetingKeys.all(workspaceId), "detail", meetingId] as const,
};

export function meetingListOptions(workspaceId: string, params: { search?: string; includeArchived?: boolean; projectId?: string; agentId?: string } = {}) {
  return queryOptions({
    queryKey: [...meetingKeys.list(workspaceId), params.search ?? "", params.includeArchived === true, params.projectId ?? "", params.agentId ?? ""] as const,
    queryFn: () => api.listMeetings(params, { workspaceId }),
    staleTime: 10_000,
  });
}

export function meetingDetailOptions(workspaceId: string, meetingId: string) {
  return queryOptions({
    queryKey: meetingKeys.detail(workspaceId, meetingId),
    queryFn: () => api.getMeeting(meetingId, { workspaceId }),
    enabled: meetingId.length > 0,
  });
}
