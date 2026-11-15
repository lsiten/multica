// @vitest-environment node

import { describe, expect, it } from "vitest";
import { parseAgentDesktopReadiness } from "./vscreen-schemas";

describe("parseAgentDesktopReadiness", () => {
  it("maps the daemon readiness response and falls back unknown permission states", () => {
    expect(
      parseAgentDesktopReadiness({
        platform: "macos",
        version: "1.3.1",
        permissions: {
          accessibility: "granted",
          screen_recording: "future-state",
          automation: "unknown",
        },
        surface_count: 9,
        ready: true,
      }),
    ).toEqual({
      platform: "macos",
      version: "1.3.1",
      permissions: {
        accessibility: "granted",
        screenRecording: "unknown",
        automation: "unknown",
      },
      surfaceCount: 9,
      ready: true,
    });
  });

  it.each([
    {
      platform: "",
      version: "1.3.1",
      permissions: {
        accessibility: "granted",
        screen_recording: "granted",
        automation: "unknown",
      },
      surface_count: 1,
      ready: true,
    },
    {
      platform: "macos",
      version: "1.3.1",
      permissions: {
        accessibility: "denied",
        screen_recording: "granted",
        automation: "unknown",
      },
      surface_count: 0,
      ready: false,
    },
    {
      platform: "macos",
      version: "1.3.1",
      permissions: {
        accessibility: "granted",
        screen_recording: "granted",
        automation: "unknown",
      },
      surface_count: 1,
      ready: true,
      reason: "contradictory",
    },
  ])("rejects malformed readiness instead of inventing state", (value) => {
    expect(parseAgentDesktopReadiness(value)).toBeNull();
  });
});
