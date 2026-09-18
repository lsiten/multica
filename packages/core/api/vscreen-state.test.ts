// @vitest-environment node
import { describe, expect, it } from "vitest";
import { scope, envelope, sourceWire, stateWire } from "./vscreen-fixtures";
import {
  parseVscreenReceipt,
  parseVscreenSources,
  parseVscreenState,
} from "./vscreen-state";

describe("Vscreen response identity", () => {
  it.each([
    { ...stateWire, runtime_id: "foreign" },
    { ...stateWire, workspace_id: "foreign" },
    { ...stateWire, state: { ...stateWire.state, runtime_id: "foreign" } },
    {
      ...stateWire,
      state: {
        ...stateWire.state,
        state_revision: Number.MAX_SAFE_INTEGER + 1,
      },
    },
    { ...stateWire, state: { ...stateWire.state, geometry_revision: 0 } },
    { ...stateWire, state: { ...stateWire.state, active_task_id: null } },
    { ...stateWire, state: { ...stateWire.state, control_state: "human" } },
    { state: stateWire.state },
  ])(
    "rejects foreign or malformed state when the native contract is invalid: %j",
    (raw) => {
      // Given / When
      const result = parseVscreenState(raw, scope);
      // Then
      expect(result).toBeNull();
    },
  );

  it("degrades unknown authority enums and missing permission observations when the backend evolves", () => {
    // Given
    const raw = {
      ...stateWire,
      state: {
        ...stateWire.state,
        state: "new_state",
        control_state: "new_control",
        permissions: undefined,
      },
    };
    // When
    const result = parseVscreenState(raw, scope);
    // Then
    expect(result?.state).toMatchObject({
      state: "unknown",
      controlState: "unknown",
      permissions: { accessibility: "unknown", screenRecording: "unknown" },
    });
  });

  it("ignores additive backend fields when parsing a valid snapshot", () => {
    // Given / When
    const result = parseVscreenState(
      { ...stateWire, new_feature: { enabled: true } },
      scope,
    );
    // Then
    expect(result).not.toHaveProperty("new_feature");
    expect(result?.state.state).toBe("ready");
  });

  it.each([
    {
      ...sourceWire,
      resource: {
        ...sourceWire.resource,
        backend_identity: "https://foreign.test",
      },
    },
    {
      ...sourceWire,
      resource: { ...sourceWire.resource, runtime_id: "foreign" },
    },
    {
      ...sourceWire,
      resource: { ...sourceWire.resource, workspace_id: "foreign" },
    },
    { ...sourceWire, source: { ...sourceWire.source, kind: "future" } },
    { ...sourceWire, generation: "" },
    { ...sourceWire, width: -1 },
  ])(
    "rejects foreign or malformed sources when the catalog cannot bind them: %j",
    (source) => {
      // Given / When
      const result = parseVscreenSources(
        { ...envelope, sources: [source] },
        scope,
      );
      // Then
      expect(result).toBeNull();
    },
  );

  it("rejects duplicate source identities when the catalog is ambiguous", () => {
    // Given / When
    const result = parseVscreenSources(
      { ...envelope, sources: [sourceWire, sourceWire] },
      scope,
    );
    // Then
    expect(result).toBeNull();
  });

  it("maps physical viewing independently from the active AI task when sources differ", () => {
    // Given
    const physical = {
      ...sourceWire,
      source: { kind: "physical", source_id: "physical-1" },
    };
    // When
    const result = parseVscreenSources(
      { ...envelope, sources: [sourceWire, physical] },
      scope,
    );
    // Then
    expect(result?.sources.map((source) => source.source.kind)).toEqual([
      "virtual",
      "physical",
    ]);
    expect(parseVscreenState(stateWire, scope)?.state.activeTaskId).toBe(
      "task-1",
    );
  });

  it.each(["running", "pending", "succeeded"])(
    "preserves receipt state %s without promoting HTTP acceptance",
    (state) => {
      // Given / When
      const result = parseVscreenReceipt(
        { ...envelope, command_id: "cmd-1", receipt_id: "receipt-1", state },
        scope,
        "cmd-1",
      );
      // Then
      expect(result?.state).toBe(state);
    },
  );

  it.each([
    { state: "succeeded", reason: "permission_denied" },
    { state: "future" },
    { state: "failed" },
    { state: "pending", command_id: "foreign" },
  ])(
    "rejects invalid receipt outcomes when identity or state contradicts: %j",
    (fields) => {
      // Given / When
      const result = parseVscreenReceipt(
        {
          ...envelope,
          command_id: "cmd-1",
          receipt_id: "receipt-1",
          ...fields,
        },
        scope,
        "cmd-1",
      );
      // Then
      expect(result).toBeNull();
    },
  );
});
