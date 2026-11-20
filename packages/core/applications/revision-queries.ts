import { infiniteQueryOptions, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { applicationKeys } from "./queries";
import type { ApplicationRevisionScope, RestoreApplicationRevisionRequest } from "./revisions";

export const applicationRevisionKeys = {
  history: (scope: ApplicationRevisionScope, currentRevision: number) => [...applicationKeys.all(scope.workspaceId), "history", scope.applicationId, currentRevision] as const,
  snapshot: (scope: ApplicationRevisionScope, revision: number) => [...applicationKeys.all(scope.workspaceId), "revision", scope.applicationId, revision] as const,
};

export function applicationRevisionHistoryOptions(scope: ApplicationRevisionScope, currentRevision: number) {
  return infiniteQueryOptions({
    queryKey: applicationRevisionKeys.history(scope, currentRevision),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => api.applicationRevisions.list(scope, { ...(pageParam > 0 ? { beforeRevision: pageParam } : {}), signal }),
    getNextPageParam: (page) => page.next_before_revision ?? undefined,
    retry: false,
  });
}

export function applicationRevisionOptions(scope: ApplicationRevisionScope, revision: number) {
  return queryOptions({
    queryKey: applicationRevisionKeys.snapshot(scope, revision),
    queryFn: ({ signal }) => api.applicationRevisions.read(scope, revision, signal),
    staleTime: Infinity,
    retry: false,
  });
}

export function useRestoreApplicationRevision(scope: ApplicationRevisionScope) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: RestoreApplicationRevisionRequest) => api.applicationRevisions.restore(scope, input),
    onSuccess: (application) => client.setQueryData(applicationKeys.detail(scope.workspaceId, scope.applicationId), application),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(scope.workspaceId) }),
  });
}
