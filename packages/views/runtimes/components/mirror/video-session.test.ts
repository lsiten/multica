// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import type { VscreenApi } from "@multica/core/api";
import { ApiClient } from "@multica/core/api";
import { MirrorVideoSession } from "./video-session";
class Peer extends EventTarget {
  static latest: Peer | null = null;
  iceGatheringState = "complete";
  localDescription = { type: "offer", sdp: "v=0" };
  connectionState = "connected";
  sctp = { maxMessageSize: 16 * 1024 };
  ontrack:
    | ((event: {
        streams: MediaStream[];
        track: { onended: (() => void) | null; stop: () => void };
      }) => void)
    | null = null;
  onconnectionstatechange: (() => void) | null = null;
  channel: {
    readyState: string;
    onopen: (() => void) | null;
    onmessage: ((event: { data: string }) => void) | null;
    onclose: (() => void) | null;
    onerror: (() => void) | null;
    send: ReturnType<typeof vi.fn>;
    close: ReturnType<typeof vi.fn>;
  } = { readyState: "open", onopen: null, onmessage: null, onclose: null, onerror: null, send: vi.fn(), close: vi.fn() };
  channels = new Map<string, typeof this.channel>();
  constructor() {
    super();
    Peer.latest = this;
  }
  addTransceiver = vi.fn();
  createDataChannel = (label: string) => {
    let channel = this.channels.get(label);
    if (!channel) {
      channel = { readyState: "open", onopen: null, onmessage: null, onclose: null, onerror: null, send: vi.fn(), close: vi.fn() };
      this.channels.set(label, channel);
    }
    if (label === "mirror-control") this.channel = channel;
    return channel;
  };
  createOffer = async () => this.localDescription;
  setLocalDescription = vi.fn(async (description: {type:string;sdp?:string}) => { this.localDescription = {type:description.type,sdp:description.sdp ?? ""}; });
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
    peer.ontrack?.({
      streams: [new MediaStream()],
      track: { onended: null, stop: vi.fn() },
    });
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

const receiveSDP = "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=recvonly\r\na=rtpmap:96 H264/90000\r\na=fmtp:96 profile-level-id=42e01f;packetization-mode=1;level-asymmetry-allowed=1\r\n";
it("sends the queried receive capability through the actual session offer", async () => {
  class ReceiverPeer extends Peer { override createOffer = async () => ({type:"offer",sdp:receiveSDP}); }
  vi.stubGlobal("RTCPeerConnection",ReceiverPeer);
  vi.stubGlobal("navigator",{mediaCapabilities:{decodingInfo:vi.fn().mockResolvedValue({supported:true,smooth:true})}});
  const createMirrorSession=vi.fn().mockResolvedValue(null);
  const api={getIceConfig:vi.fn().mockResolvedValue({iceServers:[]}),createMirrorSession} as unknown as VscreenApi;
  const session=new MirrorVideoSession({api,binding,callbacks:{state:vi.fn(),stream:vi.fn(),metadata:vi.fn()}});
  await session.start();
  expect(Peer.latest?.setLocalDescription).toHaveBeenCalledWith({type:"offer",sdp:receiveSDP.replace("level-asymmetry-allowed=1","level-asymmetry-allowed=1;max-recv-level=e028")});
  expect(createMirrorSession.mock.calls[0]?.[0].offer.sdp).toContain("max-recv-level=e028");
  await session.close();
});
it("closing during capability lookup never sets an offer or creates a remote session", async () => {
  class ReceiverPeer extends Peer { override createOffer = async () => ({type:"offer",sdp:receiveSDP}); }
  vi.stubGlobal("RTCPeerConnection",ReceiverPeer);
  const query=vi.fn(()=>new Promise(()=>{}));vi.stubGlobal("navigator",{mediaCapabilities:{decodingInfo:query}});
  const createMirrorSession=vi.fn();
  const api={getIceConfig:vi.fn().mockResolvedValue({iceServers:[]}),createMirrorSession} as unknown as VscreenApi;
  const session=new MirrorVideoSession({api,binding,callbacks:{state:vi.fn(),stream:vi.fn(),metadata:vi.fn()}});
  const starting=session.start();await vi.waitFor(()=>expect(query).toHaveBeenCalled());
  await session.close();await starting;
  expect(Peer.latest?.setLocalDescription).not.toHaveBeenCalled();expect(createMirrorSession).not.toHaveBeenCalled();expect(Peer.latest?.close).toHaveBeenCalled();
});
it("continues with the original browser offer when higher receive support is denied", async () => {
  class ReceiverPeer extends Peer { override createOffer = async () => ({type:"offer",sdp:receiveSDP}); }
  vi.stubGlobal("RTCPeerConnection",ReceiverPeer);
  vi.stubGlobal("navigator",{mediaCapabilities:{decodingInfo:vi.fn().mockResolvedValue({supported:false,smooth:false})}});
  const createMirrorSession=vi.fn().mockResolvedValue(null);
  const api={getIceConfig:vi.fn().mockResolvedValue({iceServers:[]}),createMirrorSession} as unknown as VscreenApi;
  const session=new MirrorVideoSession({api,binding,callbacks:{state:vi.fn(),stream:vi.fn(),metadata:vi.fn()}});
  await session.start();
  expect(createMirrorSession.mock.calls[0]?.[0].offer).toEqual({type:"offer",sdp:receiveSDP});
  await session.close();
});


it("encodes wheel input and fragments voice within the negotiated channel limit", async () => {
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
  const grantWire = () => ({
    grant_id: "grant-1",
    session_id: "session-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    user_id: "user-1",
    viewer_id: viewerId,
    native_epoch: "native-1",
    source: sourceWire.source,
    source_generation: "capture-1",
    expires_at: new Date(Date.now() + 30_000).toISOString(),
  });
  const sessionResponse = () => ({
    id: "session-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    user_id: "user-1",
    viewer_id: viewerId,
    daemon_id: "daemon-1",
    created_at: new Date().toISOString(),
    expires_at: grantWire().expires_at,
    state: "answered",
    answer: { type: "answer", sdp: "v=0" },
    ice_config: { ice_servers: [], turn_configured: false },
    viewer_grant: {
      grant_id: "viewer-grant-1",
      session_id: "session-1",
      workspace_id: "ws-1",
      runtime_id: "runtime-1",
      user_id: "user-1",
      viewer_id: viewerId,
      native_epoch: "native-1",
      source: sourceWire.source,
      source_generation: "capture-1",
      expires_at: grantWire().expires_at,
    },
  });
  const fetchMock = vi
    .fn<typeof globalThis.fetch>()
    .mockImplementation(async (url, init) => {
      const path = String(url);
      if (path.endsWith("/config")) {
        return new Response(
          JSON.stringify({ ice_servers: [], turn_configured: false }),
        );
      }
      if (init?.method === "POST" && path.endsWith("/mirror/sessions") && typeof init.body === "string") {
        const input: unknown = JSON.parse(init.body);
        if (input && typeof input === "object" && "viewer_id" in input && typeof input.viewer_id === "string") {
          viewerId = input.viewer_id;
        }
      }
      if (path.endsWith("/control-grants") && init?.method === "POST") {
        return new Response(JSON.stringify(grantWire()));
      }
      if (init?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify(sessionResponse()));
    });
  vi.stubGlobal("fetch", fetchMock);

  const session = new MirrorVideoSession({
    api: new ApiClient(scope.backendIdentity).vscreen(scope),
    binding,
    callbacks: {
      state: vi.fn(),
      stream: vi.fn(),
      metadata: vi.fn(),
      control: vi.fn(),
    },
  });
  await session.start();
  const peer = Peer.latest;
  if (!peer) throw new Error("Peer was not created");

  peer.ontrack?.({
    streams: [new MediaStream()],
    track: { onended: null, stop: vi.fn() },
  });
  const controlChannel = peer.channels.get("mirror-control");
  if (!controlChannel) throw new Error("Control channel missing");
  controlChannel.onopen?.();
  controlChannel.onmessage?.({
    data: JSON.stringify({
      type: "mirror:video-meta",
      source_binding: sourceWire,
      geometry_revision: 1,
      pts_nanos: 100,
      quality: { width: 640, height: 360, fps: 10, bitrate: 1_000_000, max_level_idc: 31 },
    }),
  });
  expect(peer.channels.has("mirror-input")).toBe(false);
  controlChannel.onmessage?.({data: JSON.stringify({
    type: "mirror-authorization:request", request_id: "approval", kind: "system",
    title: "Permission", message: "Approve this request", expires_at: new Date(Date.now() + 60_000).toISOString(),
  })});
  const decision = session.respondAuthorization("approval", true);
  let settled = false;
  void decision.then(() => { settled = true; });
  await Promise.resolve();
  expect(settled).toBe(false);
  controlChannel.onmessage?.({data: JSON.stringify({type: "mirror-authorization:result", request_id: "foreign", processed: true})});
  await Promise.resolve();
  expect(settled).toBe(false);
  controlChannel.onmessage?.({data: JSON.stringify({type: "mirror-authorization:result", request_id: "approval", processed: false})});
  expect(await decision).toBe(false);
  expect(await session.respondAuthorization("approval", true)).toBe(false);

  const controlRequest = session.startControl();
  await Promise.resolve();
  const inputChannel = peer.channels.get("mirror-input");
  if (!inputChannel) throw new Error("Input channel was not created on control request");
  inputChannel.onopen?.();
  await controlRequest;

  session.sendInput({
    kind: "wheel",
    gestureId: "gesture-1",
    pointer: { x: 1, y: 2, deltaX: 3, deltaY: 4 },
  });
  expect(inputChannel.send).toHaveBeenCalledOnce();
  const message = JSON.parse(
    new TextDecoder().decode(inputChannel.send.mock.calls[0]?.[0] as Uint8Array),
  );
  expect(message.pointer).toEqual({
    x: 1,
    y: 2,
    delta_x: 3,
    delta_y: 4,
  });
  await session.stopControl();
  await session.sendVoice(new Blob([new Uint8Array(96 * 1024)], { type: "audio/webm" }));
  const voice = peer.channels.get("mirror-voice");
  if (!voice) throw new Error("Voice channel missing");
  expect(voice.send.mock.calls.length).toBeGreaterThan(1);
  let offset = 0;
  const payloads: Uint8Array[] = [];
  for (const [packet] of voice.send.mock.calls) {
    expect(packet).toBeInstanceOf(Uint8Array);
    expect(packet.byteLength).toBeLessThanOrEqual(16 * 1024);
    const header = new DataView(packet.buffer, packet.byteOffset, packet.byteLength);
    expect(header.getUint32(0)).toBe(0x4d564331);
    expect(header.getUint32(8)).toBe(offset);
    payloads.push(packet.subarray(12));
    offset += packet.byteLength - 12;
  }
  const joined = new Uint8Array(offset);
  offset = 0;
  for (const payload of payloads) {
    joined.set(payload, offset);
    offset += payload.length;
  }
  expect(JSON.parse(new TextDecoder().decode(joined)).audio_base64).toHaveLength(128 * 1024);
  voice.send.mockClear();
  peer.sctp.maxMessageSize = 4096;
  await session.sendVoice(new Blob([new Uint8Array(8192)], { type: "audio/webm" }));
  expect(voice.send.mock.calls.length).toBeGreaterThan(1);
  for (const [packet] of voice.send.mock.calls) {
    expect(packet.byteLength).toBeLessThanOrEqual(4096);
  }
  await session.close();
});
