// @vitest-environment jsdom
// QueryObserver disables polling in a server environment.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { focusManager, QueryObserver } from "@tanstack/react-query";
import { api } from "../api";
import { createQueryClient } from "../query-client";
import type { AgentRuntime } from "../types";
import { runtimeListOptions } from "./queries";

vi.mock("../api", () => ({ api: { listRuntimes: vi.fn() } }));

const offlineRuntime: AgentRuntime = {
  id: "runtime-1",
  workspace_id: "workspace-1",
  daemon_id: "daemon-1",
  name: "Codex (Remote Mac)",
  runtime_mode: "local",
  provider: "codex",
  launch_header: "",
  status: "offline",
  device_info: "Remote Mac",
  metadata: {},
  owner_id: "user-1",
  visibility: "private",
  last_seen_at: "2026-09-10T05:30:00Z",
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-10T05:30:00Z",
};
const onlineRuntime: AgentRuntime = {
  ...offlineRuntime,
  status: "online",
  last_seen_at: "2026-09-10T05:40:00Z",
};

describe("runtime presence refresh", () => {
  const queryClient = createQueryClient();
  let unsubscribe = () => {};

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-10T05:40:00Z"));
    vi.mocked(api.listRuntimes).mockReset().mockResolvedValue([onlineRuntime]);
    focusManager.setFocused(true);
    queryClient.mount();
  });

  afterEach(() => {
    unsubscribe();
    queryClient.unmount();
    queryClient.clear();
    focusManager.setFocused(undefined);
    vi.useRealTimers();
  });

  it.each([undefined, "me"] as const)(
    "recovers an offline cache without a WebSocket event (owner=%s)",
    async (owner) => {
      const options = runtimeListOptions("workspace-1", owner);
      queryClient.setQueryData(options.queryKey, [offlineRuntime]);
      const observer = new QueryObserver(queryClient, options);
      unsubscribe = observer.subscribe(() => {});
      expect(api.listRuntimes).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(30_000);

      expect(observer.getCurrentResult().data).toEqual([onlineRuntime]);
      expect(api.listRuntimes).toHaveBeenCalledWith(
        { workspace_id: "workspace-1", owner },
        undefined,
      );
    },
  );

  it("refreshes stale presence when the app returns to the foreground", async () => {
    focusManager.setFocused(false);
    const options = runtimeListOptions("workspace-1");
    queryClient.setQueryData(options.queryKey, [offlineRuntime]);
    const observer = new QueryObserver(queryClient, options);
    unsubscribe = observer.subscribe(() => {});
    await vi.advanceTimersByTimeAsync(90_000);
    expect(api.listRuntimes).not.toHaveBeenCalled();

    focusManager.setFocused(true);
    await vi.advanceTimersByTimeAsync(0);

    expect(observer.getCurrentResult().data).toEqual([onlineRuntime]);
    expect(api.listRuntimes).toHaveBeenCalledTimes(1);
  });

  it("stops polling when the runtime query is no longer observed", async () => {
    const options = runtimeListOptions("workspace-1");
    queryClient.setQueryData(options.queryKey, [offlineRuntime]);
    const observer = new QueryObserver(queryClient, options);
    unsubscribe = observer.subscribe(() => {});

    unsubscribe();
    await vi.advanceTimersByTimeAsync(90_000);

    expect(api.listRuntimes).not.toHaveBeenCalled();
  });
});
