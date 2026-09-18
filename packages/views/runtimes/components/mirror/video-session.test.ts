// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "@multica/core/api";
import { MirrorVideoSession } from "./video-session";
class Peer extends EventTarget {
  static latest: Peer | null = null;
  iceGatheringState = "complete";
  localDescription = { type: "offer", sdp: "v=0" };
  connectionState = "connected";
  ontrack:
    | ((event: {
        streams: MediaStream[];
        track: { onended: (() => void) | null };
      }) => void)
    | null = null;
  onconnectionstatechange: (() => void) | null = null;
  channel: {
    onopen: (() => void) | null;
    onmessage: ((event: { data: string }) => void) | null;
    onclose: (() => void) | null;
    onerror: (() => void) | null;
  } = { onopen: null, onmessage: null, onclose: null, onerror: null };
  constructor() {
    super();
    Peer.latest = this;
  }
  addTransceiver = vi.fn();
  createDataChannel = () => this.channel;
  createOffer = async () => this.localDescription;
  setLocalDescription = async () => {};
  setRemoteDescription = async () => {};
  close = vi.fn();
}
const scope = {
  backendIdentity: "https://fixture.test",
  accountId: "user-1",
  workspaceId: "ws-1",
  runtimeId: "runtime-1",
};
const binding = {
  resource: { ...scope, uid: 501 },
  source: { kind: "virtual" as const, sourceId: "screen-1" },
  nativeEpoch: "native-1",
  generation: "capture-1",
  primary: false,
};
const sourceWire = {
  resource: {
    backend_identity: scope.backendIdentity,
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    uid: 501,
  },
  source: { kind: "virtual", source_id: "screen-1" },
  native_epoch: "native-1",
  generation: "capture-1",
  primary: false,
};
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Peer.latest = null;
});

describe("Video viewer lease lifecycle", () => {
  it("resumes renewal after a delayed control connection without claiming a decoded frame", async () => {
    // Given
    vi.useFakeTimers();
    vi.stubGlobal("RTCPeerConnection", Peer);
    vi.stubGlobal(
      "MediaStream",
      class {
        getTracks() {
          return [];
        }
      },
    );
    let viewerId = "";
    const grant = () => ({
      grant_id: "grant-1",
      session_id: "session-1",
      workspace_id: "ws-1",
      runtime_id: "runtime-1",
      user_id: "user-1",
      viewer_id: viewerId,
      native_epoch: "native-1",
      source: sourceWire.source,
      source_generation: "capture-1",
      expires_at: new Date(Date.now() + 30000).toISOString(),
    });
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockImplementation(async (url, init) => {
        const path = String(url);
        if (path.endsWith("/config"))
          return new Response(
            JSON.stringify({ ice_servers: [], turn_configured: false }),
          );
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        if (path.endsWith("/renew"))
          return new Response(JSON.stringify(grant()));
        if (typeof init?.body === "string") {
          const input: unknown = JSON.parse(init.body);
          if (
            input &&
            typeof input === "object" &&
            "viewer_id" in input &&
            typeof input.viewer_id === "string"
          )
            viewerId = input.viewer_id;
        }
        return new Response(
          JSON.stringify({
            id: "session-1",
            workspace_id: "ws-1",
            runtime_id: "runtime-1",
            user_id: "user-1",
            viewer_id: viewerId,
            daemon_id: "daemon-1",
            created_at: new Date().toISOString(),
            expires_at: grant().expires_at,
            state: "answered",
            answer: { type: "answer", sdp: "v=0" },
            ice_config: { ice_servers: [], turn_configured: false },
            viewer_grant: grant(),
          }),
        );
      });
    vi.stubGlobal("fetch", fetch);
    const state = vi.fn();
    const session = new MirrorVideoSession({
      api: new ApiClient(scope.backendIdentity).vscreen(scope),
      binding,
      callbacks: { state, stream: vi.fn(), metadata: vi.fn() },
    });
    await session.start();
    const peer = Peer.latest;
    if (!peer) throw new Error("Peer was not created");
    // When
    await vi.advanceTimersByTimeAsync(11000);
    peer.ontrack?.({ streams: [new MediaStream()], track: { onended: null } });
    peer.channel.onopen?.();
    peer.channel.onmessage?.({
      data: JSON.stringify({
        type: "mirror:video-meta",
        source_binding: sourceWire,
        geometry_revision: 1,
        pts_nanos: 100,
        quality: {
          width: 640,
          height: 360,
          fps: 10,
          bitrate: 1000000,
          max_level_idc: 31,
        },
      }),
    });
    await vi.advanceTimersByTimeAsync(10000);
    // Then
    expect(
      fetch.mock.calls.filter(([url]) => String(url).endsWith("/renew")),
    ).toHaveLength(1);
    expect(state).toHaveBeenLastCalledWith("decoding");
    expect(peer.addTransceiver).toHaveBeenCalledWith("video", {
      direction: "recvonly",
    });
    await session.close();
    expect(vi.getTimerCount()).toBe(0);
  });
});
