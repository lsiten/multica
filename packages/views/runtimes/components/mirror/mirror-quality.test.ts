// @vitest-environment node

import { describe, expect, it } from "vitest";
import { selectAdaptiveVideoProfile } from "./mirror-quality";

describe("mirror adaptive quality", () => {
  it("moves to saver quality when loss or decoded frame rate collapses", () => {
    expect(selectAdaptiveVideoProfile({
      current: "smooth",
      roundTripTimeMs: 90,
      packetLossPercent: 9,
      framesPerSecond: 24,
      targetFramesPerSecond: 30,
    })).toBe("saver");
    expect(selectAdaptiveVideoProfile({
      current: "smooth",
      roundTripTimeMs: 90,
      packetLossPercent: 0.2,
      framesPerSecond: 8,
      targetFramesPerSecond: 30,
    })).toBe("saver");
  });

  it("reaches clear quality only with stable network and near-target decoding", () => {
    expect(selectAdaptiveVideoProfile({
      current: "smooth",
      roundTripTimeMs: 45,
      packetLossPercent: 0,
      framesPerSecond: 30,
      targetFramesPerSecond: 30,
    })).toBe("clear");
    expect(selectAdaptiveVideoProfile({
      current: "smooth",
      roundTripTimeMs: 90,
      packetLossPercent: 0,
      framesPerSecond: 30,
      targetFramesPerSecond: 30,
    })).toBe("smooth");
  });
});
