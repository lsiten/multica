import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ProgressFilters, ProgressScope } from "../types/issue-progress";

export const issueProgressKeys = {
  all: (workspaceId: string) => ["issue-progress", workspaceId] as const,
  scope: (workspaceId: string, memberId: string, scope: ProgressScope) => [...issueProgressKeys.all(workspaceId), memberId, scope.type, scope.id] as const,
  view: (workspaceId: string, memberId: string, scope: ProgressScope, filters: ProgressFilters) => [...issueProgressKeys.scope(workspaceId, memberId, scope), filters] as const,
};

export function isProgressInvalidationEvent(type: string): boolean {
  return /^(issue:(created|updated|deleted)|issue_status:changed|task:(queued|dispatch|running|waiting_local_directory|completed|failed|cancelled)|human-request:changed|project:updated|project_supervision:updated|agent:(status|updated|archived|restored|deleted)|squad:(updated|deleted)|daemon:(register|offline)|runtime:(updated|status))$/.test(type);
}

export function issueProgressOptions(workspaceId: string, memberId: string, issueId: string) {
  const scope: ProgressScope = { type: "issue", id: issueId };
  return queryOptions({
    queryKey: [...issueProgressKeys.scope(workspaceId, memberId, scope), "summary"] as const,
    queryFn: async ({ signal }) => {
      const view = await api.getWorkProgress(scope, { limit: 1 }, undefined, signal);
      if (view.workspace_id !== workspaceId || view.scope.id !== issueId || view.scope.type !== "issue") throw new Error("Progress response belongs to another scope");
      return view;
    },
    enabled: !!workspaceId && !!memberId && !!issueId,
    staleTime: 15_000, refetchOnMount: "always", refetchOnWindowFocus: true, refetchInterval: 30_000,
  });
}

export function workProgressInfiniteOptions(workspaceId: string, memberId: string, scope: ProgressScope, filters: ProgressFilters) {
  return infiniteQueryOptions({
    queryKey: issueProgressKeys.view(workspaceId, memberId, scope, filters),
    queryFn: async ({ pageParam, signal }) => {
      const view = await api.getWorkProgress(scope, filters, pageParam, signal);
      if (view.workspace_id !== workspaceId || view.scope.id !== scope.id || view.scope.type !== scope.type) throw new Error("Progress response belongs to another scope");
      return view;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: page => page.has_more ? page.next_cursor ?? undefined : undefined,
    enabled: !!workspaceId && !!memberId && !!scope.id,
    staleTime: 15_000, refetchOnMount: "always", refetchOnWindowFocus: true, refetchInterval: 30_000,
    retry: (failures, error) => !("status" in error && (error.status === 400 || error.status === 404 || error.status === 409)) && failures < 2,
  });
}
