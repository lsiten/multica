// @vitest-environment node

import { describe, expect, it } from "vitest";
import {
  RUNTIME_MIRROR_AVAILABILITY,
  runtimeMirrorAvailability,
  SCREEN_MIRROR_CAPABILITY_V1,
} from "./mirror";
import type { RuntimeDevice } from "../types";

function runtime(overrides: Partial<RuntimeDevice>): RuntimeDevice {
  return {
    id: "runtime-1",
    workspace_id: "workspace-1",
    daemon_id: "daemon-1",
    name: "Runtime",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

describe("runtimeMirrorAvailability", () => {
  it("supports an online daemon advertising the capability", () => {
    const result = runtimeMirrorAvailability(
      runtime({ metadata: { capabilities: [SCREEN_MIRROR_CAPABILITY_V1], client_os: "macos" } }),
    );
    expect(result.kind).toBe(RUNTIME_MIRROR_AVAILABILITY.supported);
    expect(result.enabled).toBe(true);
  });

  it("marks an online Linux daemon as unsupported without capture", () => {
    const result = runtimeMirrorAvailability(runtime({ metadata: { client_os: "linux" } }));
    expect(result.kind).toBe(RUNTIME_MIRROR_AVAILABILITY.linuxUnsupported);
  });

  it("marks an online older macOS daemon as outdated", () => {
    const result = runtimeMirrorAvailability(runtime({ metadata: { client_os: "macos" } }));
    expect(result.kind).toBe(RUNTIME_MIRROR_AVAILABILITY.oldDaemon);
  });

  it("marks an offline runtime as offline", () => {
    const result = runtimeMirrorAvailability(
      runtime({ status: "offline", metadata: { capabilities: [SCREEN_MIRROR_CAPABILITY_V1] } }),
    );
    expect(result.kind).toBe(RUNTIME_MIRROR_AVAILABILITY.offline);
  });
});
