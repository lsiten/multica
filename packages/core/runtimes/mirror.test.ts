// @vitest-environment node

import { describe, expect, it } from "vitest";
import {
  RUNTIME_MIRROR_AVAILABILITY,
  runtimeMirrorAvailability,
  SCREEN_MIRROR_CAPABILITY_V1,
  selectRuntimeForMirror,
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


describe("selectRuntimeForMirror", () => {
  it("prefers an online readable runtime that advertises mirror capture", () => {
    const oldDaemon = runtime({
      id: "old-online",
      metadata: { client_os: "macos" },
    });
    const supported = runtime({
      id: "supported-online",
      metadata: {
        client_os: "macos",
        capabilities: [SCREEN_MIRROR_CAPABILITY_V1],
      },
    });

    expect(selectRuntimeForMirror([oldDaemon, supported], "user-1"))
      .toBe(supported);
  });

  it("falls back to an online old daemon so the device action can explain why it is disabled", () => {
    const offlineSupported = runtime({
      id: "offline-supported",
      status: "offline",
      metadata: { capabilities: [SCREEN_MIRROR_CAPABILITY_V1] },
    });
    const onlineOldDaemon = runtime({
      id: "online-old",
      metadata: { client_os: "macos" },
    });

    expect(selectRuntimeForMirror([offlineSupported, onlineOldDaemon], "user-1"))
      .toBe(onlineOldDaemon);
  });

  it("ignores private runtimes owned by another user", () => {
    const teammateRuntime = runtime({
      id: "teammate-runtime",
      owner_id: "user-2",
      metadata: {
        client_os: "macos",
        capabilities: [SCREEN_MIRROR_CAPABILITY_V1],
      },
    });

    expect(selectRuntimeForMirror([teammateRuntime], "user-1")).toBeNull();
  });
});
