import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const collaborationGraphKeys = {
  project: (wsId: string, projectId: string, offset = 0, status = "") => ["collaboration-graph", "project", wsId, projectId, offset, status] as const,
  squad: (wsId: string, squadId: string) => ["collaboration-graph", "squad", wsId, squadId] as const,
};

export function projectCollaborationGraphOptions(wsId: string, projectId: string, params?: { offset?: number; status?: string }) {
  const offset = params?.offset ?? 0;
  const status = params?.status ?? "";
  return queryOptions({
    queryKey: collaborationGraphKeys.project(wsId, projectId, offset, status),
    queryFn: () => api.getProjectCollaborationGraph(projectId, { offset, status: status || undefined }),
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}

export function projectCollaborationGraphInfiniteOptions(wsId: string, projectId: string, status = "") {
  return infiniteQueryOptions({
    queryKey: ["collaboration-graph", "project", wsId, projectId, "infinite", status] as const,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => api.getProjectCollaborationGraph(projectId, { offset: pageParam, status: status || undefined }),
    getNextPageParam: (page) => page.has_more ? page.offset + Math.max(1, page.limit) : undefined,
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}

export function projectCollaborationEvidenceOptions(wsId: string, projectId: string, edgeId?: string) {
  return queryOptions({
    queryKey: ["collaboration-graph", "project-evidence", wsId, projectId, edgeId ?? "all"] as const,
    queryFn: () => api.getProjectCollaborationEvidence(projectId, edgeId ? { edge_id: edgeId } : undefined),
    enabled: Boolean(edgeId),
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}

export function projectCollaborationEvidenceInfiniteOptions(wsId: string, projectId: string, edgeId?: string, status = "") {
  return infiniteQueryOptions({
    queryKey: ["collaboration-graph", "project-evidence", wsId, projectId, "infinite", edgeId ?? "all", status] as const,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => api.getProjectCollaborationEvidence(projectId, { edge_id: edgeId, offset: pageParam, status: status || undefined }),
    getNextPageParam: (page) => page.has_more ? page.offset + Math.max(1, page.limit) : undefined,
    enabled: Boolean(edgeId),
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}

export function squadCollaborationGraphOptions(wsId: string, squadId: string) {
  return queryOptions({
    queryKey: collaborationGraphKeys.squad(wsId, squadId),
    queryFn: () => api.getSquadCollaborationGraph(squadId),
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}

export function squadCollaborationHistoryOptions(wsId: string, squadId: string, revision: number | null) {
  return queryOptions({
    queryKey: [...collaborationGraphKeys.squad(wsId, squadId), "history", revision] as const,
    queryFn: () => api.getSquadCollaborationGraph(squadId, revision ?? undefined),
    enabled: revision !== null,
    staleTime: Infinity,
  });
}
