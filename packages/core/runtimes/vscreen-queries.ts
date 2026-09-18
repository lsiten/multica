import {
  queryOptions,
  useMutation,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { getApi } from "../api";
import type {
  VscreenCommand,
  VscreenScope,
  VscreenStateResponse,
} from "../types/vscreen";

export const vscreenKeys = {
  all: (scope: VscreenScope) =>
    [
      "vscreen",
      scope.backendIdentity,
      scope.accountId,
      scope.workspaceId,
      scope.runtimeId,
    ] as const,
  state: (scope: VscreenScope) => [...vscreenKeys.all(scope), "state"] as const,
  sources: (scope: VscreenScope) =>
    [...vscreenKeys.all(scope), "sources"] as const,
  command: (scope: VscreenScope, commandId: string) =>
    [...vscreenKeys.all(scope), "command", commandId] as const,
};

/** A daemon restart resets revision ordering; responses from one generation cannot regress it. */
export function newestVscreenState(
  previous: VscreenStateResponse | null | undefined,
  incoming: VscreenStateResponse | null,
): VscreenStateResponse | null {
  if (!incoming) return null;
  if (
    previous &&
    previous.workspaceId === incoming.workspaceId &&
    previous.runtimeId === incoming.runtimeId &&
    previous.daemonGeneration === incoming.daemonGeneration &&
    previous.state.nativeEpoch === incoming.state.nativeEpoch &&
    previous.state.stateRevision >= incoming.state.stateRevision
  )
    return previous;
  return incoming;
}

/** A late request cannot replace an epoch observed while that request was in flight. */
export function reconcileVscreenObservation(observations: {
  readonly atStart: VscreenStateResponse | null | undefined;
  readonly current: VscreenStateResponse | null | undefined;
  readonly incoming: VscreenStateResponse | null;
}): VscreenStateResponse | null {
  const { atStart, current, incoming } = observations;
  const epochChangedDuringRequest =
    current &&
    (current.daemonGeneration !== atStart?.daemonGeneration ||
      current.state.nativeEpoch !== atStart?.state.nativeEpoch);
  if (
    epochChangedDuringRequest &&
    incoming &&
    (incoming.daemonGeneration !== current.daemonGeneration ||
      incoming.state.nativeEpoch !== current.state.nativeEpoch)
  )
    return current;
  return newestVscreenState(current, incoming);
}

export function vscreenStateOptions(
  scope: VscreenScope,
  queryClient: QueryClient,
) {
  const client = getApi().vscreen(scope);
  const queryKey = vscreenKeys.state(scope);
  return queryOptions({
    queryKey,
    queryFn: async ({ signal }) => {
      const atStart = queryClient.getQueryData<VscreenStateResponse | null>(
        queryKey,
      );
      const incoming = await client.getState(signal);
      const current = queryClient.getQueryData<VscreenStateResponse | null>(
        queryKey,
      );
      return reconcileVscreenObservation({ atStart, current, incoming });
    },
    staleTime: 0,
    refetchInterval: 5_000,
    retry: false,
  });
}

export function vscreenSourcesOptions(scope: VscreenScope) {
  const client = getApi().vscreen(scope);
  return queryOptions({
    queryKey: vscreenKeys.sources(scope),
    queryFn: ({ signal }) => client.getSources(signal),
    staleTime: 0,
    refetchInterval: 5_000,
    retry: false,
  });
}

export function vscreenCommandOptions(scope: VscreenScope, commandId: string) {
  const client = getApi().vscreen(scope);
  return queryOptions({
    queryKey: vscreenKeys.command(scope, commandId),
    queryFn: ({ signal }) => client.getCommand(commandId, signal),
    retry: false,
    refetchInterval: (query) =>
      ["pending", "running"].includes(query.state.data?.state ?? "")
        ? 1_000
        : false,
  });
}

export function useVscreenCommand(scope: VscreenScope) {
  const queryClient = useQueryClient();
  const client = getApi().vscreen(scope);
  return useMutation({
    mutationKey: [...vscreenKeys.all(scope), "command"],
    mutationFn: (command: VscreenCommand) => client.createCommand(command),
    retry: false,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: vscreenKeys.all(scope) }),
  });
}

export function vscreenInterventionsOptions(scope: VscreenScope) {
  const api = getApi().vscreen(scope);
  return queryOptions({ queryKey: [...vscreenKeys.all(scope), "interventions"] as const,
    queryFn: ({ signal }) => api.interventions.list(signal), refetchInterval: 2_000, retry: false });
}
