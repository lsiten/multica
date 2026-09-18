// @vitest-environment jsdom

import { useMemo } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MirrorICEConfig, MirrorSessionResponse, VscreenScope } from "@multica/core/types";
import { useRuntimeMirrorSession } from "./use-runtime-mirror-session";

const mocks = vi.hoisted(() => ({
  getICEConfig: vi.fn(),
  createSession: vi.fn(),
  getSession: vi.fn(),
  closeSession: vi.fn(),
  scopedClient: vi.fn(),
  peers: [] as FakePeer[],
}));

vi.mock("@multica/core/api", () => ({
  getApi: () => ({ vscreen: mocks.scopedClient }),
  api: {
    getMirrorICEConfig: mocks.getICEConfig,
    createMirrorSession: mocks.createSession,
    getMirrorSession: mocks.getSession,
    closeMirrorSession: mocks.closeSession,
  },
  ApiError: class extends Error {
    constructor(readonly status: number) {
      super(`API error: ${status}`);
      this.name = "ApiError";
    }
  },
}));

const emptyICEConfig: MirrorICEConfig = {
  ice_servers: [],
  turn_configured: false,
};

function session(patch: Partial<MirrorSessionResponse>): MirrorSessionResponse {
  return {
    id: "session-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    user_id: "user-1",
    daemon_id: "daemon-1",
    viewer_id: "viewer-1",
    created_at: "2026-09-12T00:00:00Z",
    expires_at: "2026-09-12T00:01:00Z",
    state: "offered",
    ice_config: emptyICEConfig,
    ...patch,
  };
}

class FakeDataChannel extends EventTarget {
  binaryType: BinaryType = "blob";
  onmessage: ((event: MessageEvent<unknown>) => void | Promise<void>) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  close = vi.fn();
}

class FakePeer extends EventTarget {
  iceGatheringState: RTCIceGatheringState = "new";
  iceConnectionState: RTCIceConnectionState = "new";
  connectionState: RTCPeerConnectionState = "new";
  localDescription: RTCSessionDescriptionInit | null = null;
  channel = new FakeDataChannel();
  oniceconnectionstatechange: (() => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;

  createDataChannel = vi.fn(() => this.channel);
  createOffer = vi.fn(async () => ({ type: "offer" as const, sdp: "initial-offer" }));
  setLocalDescription = vi.fn(async (description: RTCSessionDescriptionInit) => {
    this.localDescription = { ...description };
  });
  setRemoteDescription = vi.fn(async () => undefined);
  close = vi.fn();

  constructor() {
    super();
    mocks.peers.push(this);
  }
}

function renderSession() {
  return renderHook(({ enabled }: { enabled: boolean }) =>
    useRuntimeMirrorSession({ runtimeId: "runtime-1", enabled }), {
    initialProps: { enabled: true },
  });
}

async function waitForPeer(): Promise<FakePeer> {
  await waitFor(() => expect(mocks.peers[0]).toBeDefined());
  const peer = mocks.peers[0];
  if (!peer) throw new Error("fake peer was not created");
  await waitFor(() => expect(peer.setLocalDescription).toHaveBeenCalled());
  return peer;
}

async function completeIceGathering(peer: FakePeer): Promise<void> {
  await act(async () => {
    peer.localDescription = { type: "offer", sdp: "gathered-offer" };
    peer.iceGatheringState = "complete";
    peer.dispatchEvent(new Event("icegatheringstatechange"));
  });
}

describe("useRuntimeMirrorSession", () => {
  beforeEach(() => {
    mocks.getICEConfig.mockReset();
    mocks.createSession.mockReset();
    mocks.getSession.mockReset();
    mocks.closeSession.mockReset();
    mocks.scopedClient.mockReset();
    mocks.peers.length = 0;
    mocks.getICEConfig.mockResolvedValue(emptyICEConfig);
    mocks.closeSession.mockResolvedValue(undefined);
    vi.stubGlobal("RTCPeerConnection", FakePeer);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("creates the signaling session after ICE gathering with the completed offer", async () => {
    mocks.createSession.mockImplementation(async () => {
      const peer = mocks.peers[0];
      expect(peer?.localDescription?.sdp).toBe("gathered-offer");
      return session({ id: "session-1" });
    });
    mocks.getSession.mockResolvedValue(session({
      id: "session-1",
      state: "answered",
      answer: { type: "answer", sdp: "remote-answer" },
    }));

    const { unmount } = renderSession();

    const firstPeer = await waitForPeer();
    expect(mocks.createSession).not.toHaveBeenCalled();
    await completeIceGathering(firstPeer);

    await waitFor(() => expect(mocks.createSession).toHaveBeenCalledTimes(1));
    expect(mocks.getICEConfig).toHaveBeenCalledBefore(mocks.createSession);
    expect(mocks.createSession).toHaveBeenCalledWith("runtime-1", expect.objectContaining({
      viewer_id: expect.any(String),
      offer: { type: "offer", sdp: "gathered-offer" },
    }));
    await waitFor(() => expect(firstPeer.setRemoteDescription).toHaveBeenCalledWith({
      type: "answer",
      sdp: "remote-answer",
    }));

    unmount();
    await waitFor(() => expect(mocks.closeSession).toHaveBeenCalledTimes(1));
  });

  it("keeps a daemon permission failure after the data channel closes", async () => {
    mocks.createSession.mockResolvedValue(session({ id: "session-1" }));
    mocks.getSession.mockResolvedValue(session({ id: "session-1", state: "offered" }));
    const { result, unmount } = renderSession();

    const peer = await waitForPeer();
    await completeIceGathering(peer);
    await waitFor(() => expect(mocks.createSession).toHaveBeenCalledTimes(1));
    const createdRequest = mocks.createSession.mock.calls[0]?.[1];
    const viewerId = createdRequest?.viewer_id;

    await act(async () => {
      await peer.channel.onmessage?.(new MessageEvent("message", {
        data: JSON.stringify({ type: "mirror:error", reason: "permission-denied" }),
      }));
    });

    await waitFor(() => {
      expect(result.current.state).toBe("failed");
      expect(result.current.failureReason).toBe("permission-denied");
    });
    act(() => peer.channel.onclose?.());
    expect(result.current.failureReason).toBe("permission-denied");
    await waitFor(() => expect(mocks.closeSession).toHaveBeenCalledTimes(1));
    expect(mocks.closeSession).toHaveBeenCalledWith("runtime-1", "session-1", viewerId);

    unmount();
    expect(mocks.closeSession).toHaveBeenCalledTimes(1);
  });
});


const scope: VscreenScope = {
  backendIdentity: "https://fixture.test",
  accountId: "user-1",
  workspaceId: "ws-1",
  runtimeId: "runtime-1",
};

function scopedClient() {
  return {
    legacy: {
      getIceConfig: vi.fn().mockResolvedValue(emptyICEConfig),
      createSession: vi.fn().mockResolvedValue(session({})),
      getSession: vi.fn().mockResolvedValue(session({ state: "answered", answer: { type: "answer", sdp: "remote-answer" } })),
      closeSession: vi.fn().mockResolvedValue(undefined),
    },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => { resolve = complete; });
  return { promise, resolve };
}

function renderScopedSession() {
  return renderHook(({ accountId, enabled }) => {
    const currentScope = useMemo(() => ({ ...scope, accountId }), [accountId]);
    return useRuntimeMirrorSession({ runtimeId: scope.runtimeId, enabled, scope: currentScope });
  }, { initialProps: { accountId: scope.accountId, enabled: true } });
}

describe("legacy scoped session teardown", () => {
  beforeEach(() => {
    mocks.scopedClient.mockReset();
    mocks.peers.length = 0;
    vi.stubGlobal("RTCPeerConnection", FakePeer);
  });
  afterEach(() => { vi.unstubAllGlobals(); });

  it("does not create an old peer when ICE resolves after account replacement", async () => {
    const pending = deferred<MirrorICEConfig>();
    const first = scopedClient();
    const second = scopedClient();
    first.legacy.getIceConfig.mockReturnValue(pending.promise);
    mocks.scopedClient.mockImplementation((value: VscreenScope) => value.accountId === scope.accountId ? first : second);
    const { rerender, unmount } = renderScopedSession();
    rerender({ accountId: "user-2", enabled: true });
    await waitForPeer();
    await act(async () => pending.resolve(emptyICEConfig));
    expect(mocks.peers).toHaveLength(1);
    expect(first.legacy.createSession).not.toHaveBeenCalled();
    unmount();
  });

  it("closes a late-created viewer through its original scoped client", async () => {
    const pending = deferred<MirrorSessionResponse>();
    const first = scopedClient();
    const second = scopedClient();
    first.legacy.createSession.mockReturnValue(pending.promise);
    mocks.scopedClient.mockImplementation((value: VscreenScope) => value.accountId === scope.accountId ? first : second);
    const { rerender, unmount } = renderScopedSession();
    const peer = await waitForPeer();
    await completeIceGathering(peer);
    await waitFor(() => expect(first.legacy.createSession).toHaveBeenCalledOnce());
    const viewerId = first.legacy.createSession.mock.calls[0]?.[0]?.viewer_id;
    rerender({ accountId: "user-2", enabled: true });
    await act(async () => pending.resolve(session({ id: "old-session" })));
    expect(first.legacy.closeSession).toHaveBeenCalledExactlyOnceWith({ sessionId: "old-session", viewerId });
    expect(second.legacy.closeSession).not.toHaveBeenCalled();
    expect(first.legacy.getSession).not.toHaveBeenCalled();
    expect(peer.close).toHaveBeenCalled();
    unmount();
  });

  it("does not apply a late answer after account replacement", async () => {
    const pending = deferred<MirrorSessionResponse>();
    const first = scopedClient();
    const second = scopedClient();
    first.legacy.getSession.mockReturnValue(pending.promise);
    mocks.scopedClient.mockImplementation((value: VscreenScope) => value.accountId === scope.accountId ? first : second);
    const { rerender, unmount } = renderScopedSession();
    const peer = await waitForPeer();
    await completeIceGathering(peer);
    await waitFor(() => expect(first.legacy.getSession).toHaveBeenCalledOnce());
    rerender({ accountId: "user-2", enabled: true });
    await act(async () => pending.resolve(session({ state: "answered", answer: { type: "answer", sdp: "late-answer" } })));
    expect(peer.setRemoteDescription).not.toHaveBeenCalled();
    expect(first.legacy.closeSession).toHaveBeenCalledOnce();
    unmount();
  });
});
