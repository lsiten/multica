// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { RuntimeDevice } from "../types";
import { scope } from "../api/vscreen-fixtures";
import { deriveVscreenAccess, VSCREEN_CAPABILITIES } from "./vscreen";

const runtime: RuntimeDevice = {
  id: scope.runtimeId,
  workspace_id: scope.workspaceId,
  daemon_id: "daemon-1",
  name: "runtime",
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

describe("mirror access", () => {
  it("requires an authenticated readable runtime and both mirror capabilities", () => {
    expect(
      deriveVscreenAccess({
        scope,
        runtime,
        authorization: { canReadRuntime: true },
      }).canView,
    ).toBe(true);
    expect(
      deriveVscreenAccess({
        scope,
        runtime: {
          ...runtime,
          metadata: { capabilities: [VSCREEN_CAPABILITIES.video] },
        },
        authorization: { canReadRuntime: true },
      }).canView,
    ).toBe(false);
  });

  it.each([
    { ...runtime, id: "foreign" },
    { ...runtime, workspace_id: "foreign" },
    { ...runtime, daemon_id: null },
    { ...runtime, status: "offline" as const },
  ])("rejects viewing when runtime identity changes: %j", (device) => {
    expect(
      deriveVscreenAccess({
        scope,
        runtime: device,
        authorization: { canReadRuntime: true },
      }).canView,
    ).toBe(false);
  });
});
