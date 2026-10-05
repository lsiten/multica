import { useEffect } from "react";
import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import type { HumanRequest, HumanRequestAnswer } from "@multica/core/types";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";

export const humanRequestKeys = {
  all: (workspaceId: string | null) => ["human-requests", workspaceId] as const,
  detail: (workspaceId: string | null, id: string) => [...humanRequestKeys.all(workspaceId), "detail", id] as const,
};

export function humanRequestOptions(workspaceId: string | null, id: string) {
  return queryOptions({ queryKey: humanRequestKeys.detail(workspaceId, id), queryFn: ({ signal }) => api.getHumanRequest(id, { signal }), enabled: !!workspaceId && !!id });
}

export function useHumanRequestSync(request?: HumanRequest) {
  const client = useQueryClient();
  const workspaceId = useWorkspaceStore(s => s.currentWorkspaceId);
  useWSSubscriptions((ws, wsId) => {
    const refresh = () => { void client.invalidateQueries({ queryKey: humanRequestKeys.all(wsId) }); };
    return [ws.on("human-request:changed", refresh), ws.onReconnect(refresh)];
  }, [client]);
  useEffect(() => {
    if (!request || request.status !== "pending") return;
    const timer = setTimeout(() => { void client.invalidateQueries({ queryKey: humanRequestKeys.detail(workspaceId, request.id) }); }, Math.max(0, Math.min(Date.parse(request.expires_at)-Date.now(), 2_147_483_647)));
    return () => clearTimeout(timer);
  }, [client, request, workspaceId]);
}

export function useRespondHumanRequest(id: string) {
  const client = useQueryClient();
  const workspaceId = useWorkspaceStore(s => s.currentWorkspaceId);
  return useMutation({ mutationFn: (answer: HumanRequestAnswer) => api.respondHumanRequest(id, answer), onSuccess: request => client.setQueryData(humanRequestKeys.detail(workspaceId, id), request), onSettled: () => client.invalidateQueries({ queryKey: humanRequestKeys.all(workspaceId) }) });
}
