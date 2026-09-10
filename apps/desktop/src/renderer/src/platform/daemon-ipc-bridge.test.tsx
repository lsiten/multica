// @vitest-environment jsdom

import type { ReactNode } from "react";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { runtimeKeys } from "@multica/core/runtimes";
import type { AgentRuntime } from "@multica/core/types";
import type { DaemonStatus } from "../../../shared/daemon-types";
import { useDaemonIPCBridge } from "./daemon-ipc-bridge";

const WORKSPACE_ID = "workspace-1";
const LOCAL_DAEMON_ID = "daemon-local";

function runtime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "runtime-local",
    workspace_id: WORKSPACE_ID,
    daemon_id: LOCAL_DAEMON_ID,
    name: "Codex (Studio Mac)",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    status: "online",
    device_info: "Studio Mac",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-09-05T00:00:00Z",
    created_at: "2026-09-05T00:00:00Z",
    updated_at: "2026-09-05T00:00:00Z",
    ...overrides,
  };
}

function mountBridge(initial: AgentRuntime[], response = initial, active = true) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  queryClient.setQueryData(runtimeKeys.list(WORKSPACE_ID), initial);
  const queryFn = vi.fn(async () => response);
  let onStatus: ((status: DaemonStatus) => void) | undefined;
  Object.defineProperty(window, "daemonAPI", {
    configurable: true,
    value: {
      onStatusChange: vi.fn((callback: (status: DaemonStatus) => void) => {
        onStatus = callback;
        return () => {};
      }),
    },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );

  const { result } = renderHook(
    () => {
      useDaemonIPCBridge(WORKSPACE_ID);
      return useQuery({
        queryKey: runtimeKeys.list(WORKSPACE_ID),
        queryFn,
        staleTime: Number.POSITIVE_INFINITY,
        enabled: active,
      });
    },
    { wrapper },
  );

  return {
    queryClient,
    queryFn,
    result,
    emit: (status: DaemonStatus) => act(() => onStatus?.(status)),
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useDaemonIPCBridge", () => {
  it("refetches runtimes when a newly registered local daemon becomes ready", async () => {
    const { emit, queryFn, result } = mountBridge([], [runtime()]);
    expect(queryFn).not.toHaveBeenCalled();

    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
    expect(result.current.data).toEqual([runtime()]);
  });

  it.each(["offline", "online"] as const)(
    "preserves the server's %s status and heartbeat timestamp when the local process is running",
    (status) => {
      const row = runtime({ status });
      const { emit, queryClient } = mountBridge([row]);

      emit({ state: "running", daemonId: LOCAL_DAEMON_ID });

      expect(queryClient.getQueryData(runtimeKeys.list(WORKSPACE_ID))).toEqual([row]);
    },
  );

  it.each(["stopped", "stopping", "auth_expired", "recovery_paused"] as const)(
    "immediately marks only the local runtime offline when its process is %s",
    (state) => {
      const local = runtime();
      const remote = runtime({ id: "runtime-remote", daemon_id: "daemon-remote" });
      const { emit, queryClient } = mountBridge([local, remote]);

      emit({ state, daemonId: LOCAL_DAEMON_ID });

      expect(queryClient.getQueryData(runtimeKeys.list(WORKSPACE_ID))).toEqual([
        { ...local, status: "offline" },
        remote,
      ]);
    },
  );

  it("refetches an existing runtime once when its daemon restarts after an identity-free stop", async () => {
    const { emit, queryFn } = mountBridge([runtime()]);
    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });
    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
    emit({ state: "stopped" });
    queryFn.mockClear();

    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
  });

  it("refetches existing runtimes when the running daemon identity changes", async () => {
    const { emit, queryFn } = mountBridge([
      runtime(),
      runtime({ id: "runtime-next", daemon_id: "daemon-next" }),
    ]);
    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });
    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
    queryFn.mockClear();

    emit({ state: "running", daemonId: "daemon-next" });

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
  });

  it("does not repeatedly refetch an existing runtime on unchanged running polls", async () => {
    const { emit, queryFn } = mountBridge([runtime()]);
    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });
    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
    queryFn.mockClear();

    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });
    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });

    expect(queryFn).not.toHaveBeenCalled();
  });

  it.each(["running", "starting", "stopped", "stopping", "auth_expired", "recovery_paused"] as const)(
    "does not renew query freshness on unchanged %s status pushes",
    (state) => {
      const { emit, queryClient } = mountBridge(
        [runtime({ status: "offline" })], undefined, false,
      );
      emit({ state, daemonId: LOCAL_DAEMON_ID });
      const updatedAt = queryClient.getQueryState(runtimeKeys.list(WORKSPACE_ID))?.dataUpdatedAt;
      vi.spyOn(Date, "now").mockReturnValue(Date.now() + 5_000);

      emit({ state, daemonId: LOCAL_DAEMON_ID });

      expect(queryClient.getQueryState(runtimeKeys.list(WORKSPACE_ID))?.dataUpdatedAt).toBe(updatedAt);
    },
  );

  it("keeps discovering runtimes while the running daemon has no server row", async () => {
    const { emit, queryFn, result } = mountBridge([]);
    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    queryFn.mockClear();

    emit({ state: "running", daemonId: LOCAL_DAEMON_ID });

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
  });
});
