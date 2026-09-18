// @vitest-environment node
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { parseVscreenState } from "../api/vscreen-state";
import { scope, stateWire } from "../api/vscreen-fixtures";
import {
  newestVscreenState,
  reconcileVscreenObservation,
  vscreenKeys,
} from "./vscreen-queries";

function state(revision: number, daemon = "daemon-1", native = "native-1") {
  const parsed = parseVscreenState(
    {
      ...stateWire,
      daemon_generation: daemon,
      state: {
        ...stateWire.state,
        state_revision: revision,
        native_epoch: native,
      },
    },
    scope,
  );
  if (!parsed) throw new Error("invalid fixture");
  return parsed;
}

describe("Vscreen query identity and monotonic observations", () => {
  it.each(["accountId", "backendIdentity", "workspaceId", "runtimeId"])(
    "isolates cached state when %s changes",
    (field) => {
      // Given
      const cache = new QueryClient();
      cache.setQueryData(vscreenKeys.state(scope), state(3));
      // When
      const other = cache.getQueryData(
        vscreenKeys.state({ ...scope, [field]: "other" }),
      );
      // Then
      expect(other).toBeUndefined();
      cache.clear();
    },
  );

  it("keeps the latest revision when a delayed same-epoch state arrives", () => {
    // Given / When
    const result = newestVscreenState(state(100), state(1));
    // Then
    expect(result?.state.stateRevision).toBe(100);
  });

  it.each([state(1, "daemon-2"), state(1, "daemon-1", "native-2")])(
    "accepts reset revision when a fresh daemon or native epoch is observed",
    (incoming) => {
      // Given / When
      const result = newestVscreenState(state(100), incoming);
      // Then
      expect(result).toBe(incoming);
    },
  );

  it.each([state(1, "daemon-2"), state(1, "daemon-1", "native-2")])(
    "rejects a late old epoch when a new observation arrived during the request",
    (current) => {
      // Given / When
      const result = reconcileVscreenObservation({
        atStart: state(99),
        current,
        incoming: state(100),
      });
      // Then
      expect(result).toBe(current);
    },
  );

  it("fails closed when malformed fresh state replaces previously valid authority", () => {
    // Given / When
    const result = newestVscreenState(state(100), null);
    // Then
    expect(result).toBeNull();
  });
});
