// @vitest-environment node
import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, setApiInstance } from "../api";
import { scope, stateWire } from "../api/vscreen-fixtures";
import { parseVscreenState } from "../api/vscreen-state";
import { vscreenKeys, vscreenStateOptions } from "./vscreen-queries";

afterEach(() => vi.unstubAllGlobals());

describe("Vscreen query request lifecycle", () => {
  it("keeps the new native epoch when an actual in-flight query returns old state", async () => {
    // Given
    const cache = new QueryClient();
    const key = vscreenKeys.state(scope);
    cache.setQueryData(key, parseVscreenState(stateWire, scope));
    setApiInstance(new ApiClient(scope.backendIdentity));
    let respond: ((response: Response) => void) | undefined;
    const response = new Promise<Response>((resolve) => {
      respond = resolve;
    });
    vi.stubGlobal("fetch", vi.fn<typeof fetch>().mockReturnValue(response));
    const current = parseVscreenState(
      {
        ...stateWire,
        state: {
          ...stateWire.state,
          native_epoch: "native-2",
          state_revision: 1,
        },
      },
      scope,
    );
    // When
    const pending = cache.fetchQuery(vscreenStateOptions(scope, cache));
    cache.setQueryData(key, current);
    respond?.(new Response(JSON.stringify(stateWire)));
    const result = await pending;
    // Then
    expect(result?.state.nativeEpoch).toBe("native-2");
    expect(result?.state.stateRevision).toBe(1);
    cache.clear();
  });

  it("keeps a newer same-epoch revision when the cache advances while a query is in flight", async () => {
    // Given
    const cache = new QueryClient();
    const key = vscreenKeys.state(scope);
    cache.setQueryData(key, parseVscreenState(stateWire, scope));
    setApiInstance(new ApiClient(scope.backendIdentity));
    let respond: ((response: Response) => void) | undefined;
    const response = new Promise<Response>((resolve) => {
      respond = resolve;
    });
    vi.stubGlobal("fetch", vi.fn<typeof fetch>().mockReturnValue(response));
    // When
    const pending = cache.fetchQuery(vscreenStateOptions(scope, cache));
    cache.setQueryData(
      key,
      parseVscreenState(
        { ...stateWire, state: { ...stateWire.state, state_revision: 10 } },
        scope,
      ),
    );
    respond?.(new Response(JSON.stringify(stateWire)));
    const result = await pending;
    // Then
    expect(result?.state.stateRevision).toBe(10);
    cache.clear();
  });
});
