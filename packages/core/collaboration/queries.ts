import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export type ProjectCollaborationFilters = { sort?: "newest" | "oldest"; node_agent_id?: string; task_query?: string; from?: string; to?: string; offset?: number; status?: string; activity?: "active" | "all" | "ended"; issue_id?: string; squad_id?: string; agent_id?: string; relation_type?: string };

export const collaborationGraphKeys = {
  project: (wsId: string, projectId: string, filters: ProjectCollaborationFilters = {}) => ["collaboration-graph", "project", wsId, projectId, filters] as const,
  squad: (wsId: string, squadId: string) => ["collaboration-graph", "squad", wsId, squadId] as const,
};

export function projectCollaborationGraphOptions(wsId: string, projectId: string, params?: ProjectCollaborationFilters) {
  const offset = params?.offset ?? 0;
  const status = params?.status ?? "";
  return queryOptions({
    queryKey: collaborationGraphKeys.project(wsId, projectId, params),
    queryFn: () => api.getProjectCollaborationGraph(projectId, { ...params, complete: true, offset, status: status || undefined }),
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

export function projectCollaborationEvidenceInfiniteOptions(wsId: string, projectId: string, edgeId?: string, filters: ProjectCollaborationFilters = {}, enabled = Boolean(edgeId)) {
  const status = filters.status ?? "";
  return infiniteQueryOptions({
    queryKey: ["collaboration-graph", "project-evidence", wsId, projectId, "infinite", edgeId ?? "all", filters] as const,
    initialPageParam: {} as { cursor?: string; snapshot_at?: string },
    queryFn: ({ pageParam }) => api.getProjectCollaborationEvidence(projectId, { edge_id: edgeId, ...filters, ...pageParam, cursor_mode: true, status: status || undefined }),
    getNextPageParam: (page) => page.has_more && page.next_cursor ? { cursor: page.next_cursor, snapshot_at: page.as_of } : undefined,
    enabled,
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
