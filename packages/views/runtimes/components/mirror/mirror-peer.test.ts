// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import {
  peerConfiguration,
  peerIceServers,
  waitForIceGatheringComplete,
} from "./mirror-peer";

class FakePeer extends EventTarget {
  iceGatheringState: RTCIceGatheringState = "gathering";
  iceConnectionState: RTCIceConnectionState = "new";
}

describe("mirror peer ICE helpers", () => {
  it("maps both scalar and array ICE server URL forms", () => {
    expect(peerIceServers([
      { urls: "stun:stun.example" },
      { urls: ["turn:turn.example", "turns:turns.example"], username: "viewer", credential: "secret" },
    ])).toEqual([
      { urls: "stun:stun.example", username: undefined, credential: undefined },
      { urls: ["turn:turn.example", "turns:turns.example"], username: "viewer", credential: "secret" },
    ]);
  });

  it("passes deployment ICE servers into the peer configuration", () => {
    const config = peerConfiguration({
      ice_servers: [{ urls: ["turn:turn.example"] }],
      turn_configured: true,
    });

    expect(config.iceServers).toEqual([
      { urls: ["turn:turn.example"], username: undefined, credential: undefined },
    ]);
  });

  it("resolves when candidate gathering completes", async () => {
    const peer = new FakePeer();
    const waiting = waitForIceGatheringComplete(peer);

    peer.iceGatheringState = "complete";
    peer.dispatchEvent(new Event("icegatheringstatechange"));

    await expect(waiting).resolves.toBeUndefined();
  });

  it("fails when candidate gathering does not complete before the deadline", async () => {
    vi.useFakeTimers();
    const peer = new FakePeer();
    const waiting = waitForIceGatheringComplete(peer, 20);
    const rejection = expect(waiting).rejects.toThrow("ICE gathering timed out");

    await vi.advanceTimersByTimeAsync(20);

    await rejection;
    vi.useRealTimers();
  });
});
