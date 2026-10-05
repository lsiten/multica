import { useCallback, useEffect } from "react";
import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWSEvent, useWSReconnect } from "../realtime";
import type { HumanRequest, HumanRequestAnswer } from "../types/human-request";
export { matchHumanTextAnswer, humanTextAnswerLabel } from "./text-answer";

export const humanRequestKeys = {
  all: (workspaceId: string) => ["human-requests", workspaceId] as const,
  detail: (workspaceId: string, id: string) => [...humanRequestKeys.all(workspaceId), "detail", id] as const,
  list: (workspaceId: string, filters: { project_id?: string; issue_id?: string; chat_session_id?: string }) => [...humanRequestKeys.all(workspaceId), "list", filters] as const,
};

export function humanRequestOptions(workspaceId: string, id: string) {
  return queryOptions({ queryKey: humanRequestKeys.detail(workspaceId, id), queryFn: ({ signal }) => api.getHumanRequest(id, signal), enabled: !!workspaceId && !!id });
}

export function humanRequestsOptions(workspaceId: string, filters: { project_id?: string; issue_id?: string; chat_session_id?: string }) {
  return queryOptions({ queryKey: humanRequestKeys.list(workspaceId, filters), queryFn: ({ signal }) => api.listHumanRequests(filters, signal), enabled: !!workspaceId });
}

export function useHumanRequestRealtime(workspaceId: string) {
  const client = useQueryClient();
  const refresh = useCallback(() => { void client.invalidateQueries({ queryKey: humanRequestKeys.all(workspaceId) }); }, [client, workspaceId]);
  useWSEvent("human-request:changed", refresh);
  useWSReconnect(refresh);
}

export function useHumanRequestExpiry(workspaceId: string, request?: HumanRequest) {
  const client = useQueryClient();
  useEffect(() => {
    if (!request || request.status !== "pending") return;
    const remaining = Date.parse(request.expires_at) - Date.now();
    // JavaScript timeouts wrap beyond about 24 days. Refetch on focus also
    // revalidates long-lived requests before a decision can be submitted.
    const timer = setTimeout(() => { void client.invalidateQueries({ queryKey: humanRequestKeys.detail(workspaceId, request.id) }); }, Math.max(0, Math.min(remaining, 2_147_483_647)));
    return () => clearTimeout(timer);
  }, [client, workspaceId, request]);
}

export function useRespondHumanRequest(workspaceId: string, id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (answer: HumanRequestAnswer) => api.respondHumanRequest(id, answer), onSuccess: request => client.setQueryData(humanRequestKeys.detail(workspaceId, id), request), onSettled: () => client.invalidateQueries({ queryKey: humanRequestKeys.all(workspaceId) }) });
}
