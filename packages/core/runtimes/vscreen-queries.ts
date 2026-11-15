import { queryOptions } from "@tanstack/react-query";
import { getApi } from "../api";
import type { VscreenScope } from "../types/vscreen";

export const vscreenKeys = {
  all: (scope: VscreenScope) =>
    [
      "vscreen",
      scope.backendIdentity,
      scope.accountId,
      scope.workspaceId,
      scope.runtimeId,
    ] as const,
  sources: (scope: VscreenScope) =>
    [...vscreenKeys.all(scope), "sources"] as const,
  agentDesktop: (scope: VscreenScope) =>
    [...vscreenKeys.all(scope), "agent-desktop"] as const,
};

export function vscreenSourcesOptions(scope: VscreenScope) {
  return queryOptions({
    queryKey: vscreenKeys.sources(scope),
    queryFn: ({ signal }) => getApi().vscreen(scope).getSources(signal),
    staleTime: 0,
    refetchInterval: 5_000,
    retry: false,
  });
}

export function agentDesktopReadinessOptions(scope: VscreenScope) {
  return queryOptions({
    queryKey: vscreenKeys.agentDesktop(scope),
    queryFn: ({ signal }) =>
      getApi().vscreen(scope).getAgentDesktopReadiness(signal),
    staleTime: 10_000,
    refetchInterval: 15_000,
    retry: false,
  });
}
