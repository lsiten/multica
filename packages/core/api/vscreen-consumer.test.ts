// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { scope, grantWire } from "./vscreen-fixtures";
import { parseVscreenMirrorSession } from "./vscreen-mirror";
import { parseVscreenSources } from "./vscreen-state";
import { envelope, sourceWire } from "./vscreen-fixtures";
afterEach(() => vi.unstubAllGlobals());
function viewer() {
  const binding = parseVscreenSources(
    { ...envelope, sources: [sourceWire] },
    scope,
  )?.sources[0];
  if (!binding) throw new Error("Invalid fixture");
  return { sessionId: "session-1", viewerId: "viewer-1", binding };
}
const session = {
  id: "session-1",
  workspace_id: scope.workspaceId,
  runtime_id: scope.runtimeId,
  user_id: scope.accountId,
  daemon_id: "daemon-1",
  viewer_id: "viewer-1",
  created_at: "2026-01-01T00:00:00Z",
  expires_at: "2099-01-01T00:00:00Z",
  state: "answered",
  answer: { type: "answer", sdp: "v=0" },
  ice_config: { ice_servers: [], turn_configured: false },
  viewer_grant: grantWire,
};
describe("Vscreen viewer consumer boundary", () => {
  it("gathers scoped ICE using the real config route when starting a viewer", async () => {
    // Given
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          ice_servers: [
            { urls: "turn:relay.example.test", username: "fixture" },
          ],
          turn_configured: true,
        }),
      ),
    );
    vi.stubGlobal("fetch", fetch);
    // When
    const config = await new ApiClient(scope.backendIdentity)
      .vscreen(scope)
      .getIceConfig();
    // Then
    expect(fetch.mock.calls[0]?.[0]).toBe(
      `${scope.backendIdentity}/api/runtimes/runtime-1/mirror/config`,
    );
    expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({
      "X-Workspace-ID": "ws-1",
      "X-Workspace-Slug": "",
    });
    expect(config?.turnConfigured).toBe(true);
  });
  it("retains negotiated output when the daemon publishes video quality", () => {
    // Given / When
    const result = parseVscreenMirrorSession(
      {
        ...session,
        video_quality: {
          width: 1280,
          height: 720,
          fps: 30,
          bitrate: 14000000,
          max_level_idc: 31,
        },
      },
      scope,
      viewer(),
    );
    // Then
    expect(result?.videoQuality).toEqual({
      width: 1280,
      height: 720,
      fps: 30,
      bitrate: 14000000,
      maxLevelIdc: 31,
    });
  });
  it("allows initial negotiation when quality is not available yet", () => {
    // Given / When
    const result = parseVscreenMirrorSession(session, scope, viewer());
    // Then
    expect(result?.videoQuality).toBeUndefined();
    expect(result?.id).toBe("session-1");
  });
  it("rejects an impossible H264 quality when the response exceeds the negotiated level", () => {
    // Given / When
    const result = parseVscreenMirrorSession(
      {
        ...session,
        video_quality: {
          width: 1600,
          height: 900,
          fps: 30,
          bitrate: 14000000,
          max_level_idc: 31,
        },
      },
      scope,
      viewer(),
    );
    // Then
    expect(result).toBeNull();
  });
  it("keeps the viewer DELETE alive when the page or native window closes", async () => {
    // Given
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    // When
    await new ApiClient(scope.backendIdentity)
      .vscreen(scope)
      .closeMirrorSession(viewer());
    // Then
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({
      method: "DELETE",
      keepalive: true,
    });
  });
});

describe("Legacy viewer scope", () => {
  it("keeps v1 request fields and explicit workspace routing without adding v2 selection", async () => {
    // Given
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(new Response(JSON.stringify(session)));
    vi.stubGlobal("fetch", fetch);
    // When
    const result = await new ApiClient(scope.backendIdentity)
      .vscreen(scope)
      .legacy.createSession({
        viewer_id: "viewer-1",
        offer: { type: "offer", sdp: "v=0" },
      });
    // Then
    expect(result.runtime_id).toBe(scope.runtimeId);
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({
      headers: { "X-Workspace-ID": scope.workspaceId, "X-Workspace-Slug": "" },
      body: JSON.stringify({
        viewer_id: "viewer-1",
        offer: { type: "offer", sdp: "v=0" },
      }),
    });
  });
  it("rejects a foreign legacy viewer response when the account changes", async () => {
    // Given
    vi.stubGlobal(
      "fetch",
      vi
        .fn<typeof globalThis.fetch>()
        .mockResolvedValue(
          new Response(JSON.stringify({ ...session, user_id: "other" })),
        ),
    );
    // When
    const result = await new ApiClient(scope.backendIdentity)
      .vscreen(scope)
      .legacy.getSession({ sessionId: "session-1", viewerId: "viewer-1" });
    // Then
    expect(result.id).toBe("");
  });
  it("invalidates captured v1 requests when the active account is replaced", async () => {
    // Given
    const api = new ApiClient(scope.backendIdentity);
    const previous = api.vscreen(scope).legacy;
    api.vscreen({ ...scope, accountId: "user-2" });
    const fetch = vi.fn<typeof globalThis.fetch>();
    vi.stubGlobal("fetch", fetch);
    // When
    const result = previous.getSession({
      sessionId: "session-1",
      viewerId: "viewer-1",
    });
    // Then
    await expect(result).rejects.toMatchObject({ name: "VscreenScopeError" });
    expect(fetch).not.toHaveBeenCalled();
  });
});
