// @vitest-environment jsdom

import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { runtimeKeys } from "@multica/core/runtimes";
import type { AgentRuntime } from "@multica/core/types";
import type { DaemonStatus } from "../../../shared/daemon-types";
import { useDaemonIPCBridge } from "./daemon-ipc-bridge";

const WORKSPACE_ID = "workspace-1";
const LOCAL_DAEMON_ID = "daemon-local";

function runtime(): AgentRuntime {
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
  };
}

describe("useDaemonIPCBridge", () => {
  it("refetches runtimes when a newly registered local daemon becomes ready", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    queryClient.setQueryData(runtimeKeys.list(WORKSPACE_ID), []);
    const queryFn = vi.fn(async () => [runtime()]);
    let emitStatus: ((status: DaemonStatus) => void) | undefined;
    Object.defineProperty(window, "daemonAPI", {
      configurable: true,
      value: {
        onStatusChange: vi.fn((callback: (status: DaemonStatus) => void) => {
          emitStatus = callback;
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
        });
      },
      { wrapper },
    );
    expect(queryFn).not.toHaveBeenCalled();

    act(() => {
      emitStatus?.({ state: "running", daemonId: LOCAL_DAEMON_ID });
    });

    await waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
    expect(result.current.data).toEqual([runtime()]);
  });
});
