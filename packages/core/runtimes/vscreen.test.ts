// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { RuntimeDevice } from "../types";
import { scope, stateWire } from "../api/vscreen-fixtures";
import { parseVscreenState } from "../api/vscreen-state";
import { deriveVscreenAccess, VSCREEN_CAPABILITIES } from "./vscreen";

const runtime: RuntimeDevice = {
  id: scope.runtimeId,
  workspace_id: scope.workspaceId,
  daemon_id: "daemon-1",
  name: "raw internal name",
  runtime_mode: "local",
  provider: "codex",
  launch_header: "",
  status: "online",
  device_info: "",
  metadata: { capabilities: Object.values(VSCREEN_CAPABILITIES) },
  owner_id: scope.accountId,
  visibility: "private",
  last_seen_at: null,
  created_at: "",
  updated_at: "",
};
const observation = parseVscreenState(stateWire, scope);
const authorized = { canReadRuntime: true, canManageRuntime: true };

describe("Vscreen authority derivation", () => {
  it("disables every control when authorization flags are absent", () => {
    // Given / When
    const result = deriveVscreenAccess({
      scope,
      runtime,
      observation,
      authorization: {},
    });
    // Then
    expect(result).toMatchObject({
      canView: false,
      canEnable: false,
      canDisable: false,
      canRequestTakeover: false,
    });
  });

  it("permits read-only viewing when a workspace member can read another owner's runtime", () => {
    // Given / When
    const result = deriveVscreenAccess({
      scope,
      runtime: { ...runtime, owner_id: "other", visibility: "public" },
      observation,
      authorization: authorized,
    });
    // Then
    expect(result).toMatchObject({
      canView: true,
      canEnable: false,
      canDisable: false,
      canRequestTakeover: false,
    });
  });

  it("keeps screen recording separate from accessibility when native permission is denied", () => {
    // Given
    const denied = parseVscreenState(
      {
        ...stateWire,
        state: {
          ...stateWire.state,
          state: "disabled",
          control_state: "idle",
          active_task_id: null,
          permissions: { screen_recording: "granted", accessibility: "denied" },
        },
      },
      scope,
    );
    // When
    const result = deriveVscreenAccess({
      scope,
      runtime,
      observation: denied,
      authorization: authorized,
    });
    // Then
    expect(result).toMatchObject({
      canView: true,
      canEnable: false,
      screenRecordingGranted: true,
      accessibilityGranted: false,
    });
  });

  it("enables a disabled screen only when owner authority and native permissions are present", () => {
    // Given
    const disabled = parseVscreenState(
      {
        ...stateWire,
        state: {
          ...stateWire.state,
          state: "disabled",
          control_state: "idle",
          active_task_id: null,
        },
      },
      scope,
    );
    // When
    const result = deriveVscreenAccess({
      scope,
      runtime,
      observation: disabled,
      authorization: authorized,
    });
    // Then
    expect(result).toMatchObject({
      canEnable: true,
      canDisable: false,
      canRequestTakeover: false,
    });
  });

  it("exposes request takeover without claiming human input authority when the agent owns control", () => {
    // Given / When
    const result = deriveVscreenAccess({
      scope,
      runtime,
      observation,
      authorization: authorized,
    });
    // Then
    expect(result).toMatchObject({
      canRequestTakeover: true,
      canReturnToAgent: false,
    });
  });

  it("disables owner controls when backend authority enums are unknown", () => {
    // Given
    const unknown = parseVscreenState(
      {
        ...stateWire,
        state: {
          ...stateWire.state,
          state: "new_state",
          control_state: "new_control",
        },
      },
      scope,
    );
    // When
    const result = deriveVscreenAccess({
      scope,
      runtime,
      observation: unknown,
      authorization: authorized,
    });
    // Then
    expect(result).toMatchObject({
      canEnable: false,
      canDisable: false,
      canRequestTakeover: false,
    });
  });

  it.each([
    { ...runtime, id: "foreign" },
    { ...runtime, workspace_id: "foreign" },
    { ...runtime, daemon_id: null },
    { ...runtime, status: "offline" as const },
  ])(
    "disables actions when runtime identity or presence differs: %j",
    (device) => {
      // Given / When
      const result = deriveVscreenAccess({
        scope,
        runtime: device,
        observation,
        authorization: authorized,
      });
      // Then
      expect(result).toMatchObject({
        canView: false,
        canEnable: false,
        canDisable: false,
        canRequestTakeover: false,
      });
    },
  );
});
