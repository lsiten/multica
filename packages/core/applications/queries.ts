import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";

export const applicationKeys = {
  all: (workspaceId: string) => ["applications", workspaceId] as const,
  board: (workspaceId: string) => [...applicationKeys.all(workspaceId), "board"] as const,
  operation: (workspaceId: string, id: string, operationId: string) => [...applicationKeys.all(workspaceId), "operation", id, operationId] as const,
  list: (workspaceId: string, projectId?: string) => [...applicationKeys.all(workspaceId), "list", projectId ?? "all"] as const,
  detail: (workspaceId: string, id: string) => [...applicationKeys.all(workspaceId), "detail", id] as const,
  plan: (workspaceId: string, id: string, revision: number) => [...applicationKeys.all(workspaceId), "plan", id, revision] as const,
};

export function applicationBoardOptions(workspaceId: string) {
  return queryOptions({
    queryKey: applicationKeys.board(workspaceId),
    queryFn: ({ signal }) => api.getApplicationBoard(workspaceId, signal),
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  });
}

export interface ApplicationLogStream {
  text: string;
  cursor: string;
  gap: boolean;
  truncated: boolean;
  lastPageHasText: boolean;
}

export function applicationLogsOptions(workspaceId: string, id: string, instanceId: string, client: QueryClient) {
  const queryKey = [...applicationKeys.all(workspaceId), "logs", id, instanceId] as const;
  return queryOptions({
    queryKey,
    queryFn: async ({ signal }): Promise<ApplicationLogStream> => {
      const previous = client.getQueryData<ApplicationLogStream>(queryKey);
      const page = await api.getApplicationLogs(workspaceId, id, instanceId, previous?.cursor ?? "", signal);
      const text = (page.gap ? "" : previous?.text ?? "") + page.text;
      const limit = 256 * 1024;
      return {
        text: text.slice(-limit), cursor: page.cursor,
        gap: page.gap || previous?.gap === true,
        truncated: text.length > limit || (!page.gap && previous?.truncated === true),
        lastPageHasText: page.text.length > 0,
      };
    },
    gcTime: 0,
    refetchInterval: (query) => query.state.data?.lastPageHasText ? 200 : 3000,
    refetchIntervalInBackground: false,
  });
}

export function applicationOperationOptions(workspaceId: string, id: string, operationId: string) {
  return queryOptions({
    queryKey: applicationKeys.operation(workspaceId, id, operationId),
    queryFn: ({ signal }) => api.getApplicationOperation(workspaceId, id, operationId, signal),
    refetchInterval: (query) => query.state.data?.state === "queued" || query.state.data?.state === "running" || query.state.data?.state === "cancelling" ? 2000 : false,
  });
}

export function applicationListOptions(workspaceId: string, projectId?: string) {
  return queryOptions({
    queryKey: applicationKeys.list(workspaceId, projectId),
    queryFn: ({ signal }) => api.listApplications(workspaceId, projectId, signal),
  });
}

export function applicationDetailOptions(workspaceId: string, id: string) {
  return queryOptions({
    queryKey: applicationKeys.detail(workspaceId, id),
    queryFn: ({ signal }) => api.getApplication(workspaceId, id, signal),
  });
}

export function applicationPlanOptions(workspaceId: string, id: string, revision: number) {
  return queryOptions({
    queryKey: applicationKeys.plan(workspaceId, id, revision),
    queryFn: ({ signal }) => api.previewApplicationPlan(workspaceId, id, signal),
  });
}
