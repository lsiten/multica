// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { setApiInstance } from "./index";
import { envelope, scope, sourceWire, grantWire } from "./vscreen-fixtures";
import { parseVscreenSources } from "./vscreen-state";
import { MirrorSessionResponseSchema } from "./schemas";
import { VscreenScopeError } from "./vscreen";

afterEach(() => vi.unstubAllGlobals());
function binding() {
  const result = parseVscreenSources(
    { ...envelope, sources: [sourceWire] },
    scope,
  )?.sources[0];
  if (!result) throw new Error("invalid fixture");
  return result;
}
const sessionWire = {
  id: "session-1",
  workspace_id: scope.workspaceId,
  runtime_id: scope.runtimeId,
  user_id: scope.accountId,
  daemon_id: "daemon-1",
  viewer_id: "viewer-1",
  created_at: "2026-01-01T00:00:00Z",
  expires_at: "2099-01-01T00:00:00Z",
  state: "offered",
  ice_config: { ice_servers: [], turn_configured: false },
};

describe("Vscreen request contracts", () => {
  it("sends an idempotent command and keeps the pending receipt when HTTP accepts it", async () => {
    // Given
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          ...envelope,
          command_id: "cmd-1",
          receipt_id: "receipt-1",
          state: "pending",
        }),
        { status: 202 },
      ),
    );
    vi.stubGlobal("fetch", fetch);
    const api = new ApiClient(scope.backendIdentity).vscreen(scope);
    // When
    const receipt = await api.createCommand({
      commandId: "cmd-1",
      kind: "enable",
    });
    // Then
    expect(receipt?.state).toBe("pending");
    expect(fetch.mock.calls[0]?.[0]).toBe(
      `${scope.backendIdentity}/api/runtimes/runtime-1/vscreen/commands`,
    );
    expect(fetch.mock.calls[0]?.[1]).toMatchObject({
      method: "POST",
      body: JSON.stringify({ command_id: "cmd-1", kind: "enable" }),
    });
  });

  it("sends v2 source selection when creating a viewer", async () => {
    // Given
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(
        new Response(
          JSON.stringify({ ...sessionWire, viewer_grant: grantWire }),
        ),
      );
    vi.stubGlobal("fetch", fetch);
    const api = new ApiClient(scope.backendIdentity).vscreen(scope);
    // When
    const session = await api.createMirrorSession({
      viewerId: "viewer-1",
      offer: { type: "offer", sdp: "v=0" },
      binding: binding(),
    });
    // Then
    expect(session?.viewerGrant?.sourceGeneration).toBe("capture-1");
    expect(fetch.mock.calls[0]?.[1]?.body).toBe(
      JSON.stringify({
        viewer_id: "viewer-1",
        offer: { type: "offer", sdp: "v=0" },
        protocol_version: 2,
        transport: "video",
        source: { kind: "virtual", source_id: "display-1" },
        source_generation: "capture-1",
      }),
    );
  });

  it("rejects foreign source selection before any HTTP request", async () => {
    // Given
    const fetch = vi.fn<typeof globalThis.fetch>();
    vi.stubGlobal("fetch", fetch);
    const source = binding();
    const api = new ApiClient(scope.backendIdentity).vscreen(scope);
    // When
    const result = api.createMirrorSession({
      viewerId: "viewer-1",
      offer: { type: "offer", sdp: "v=0" },
      binding: {
        ...source,
        resource: { ...source.resource, runtimeId: "foreign" },
      },
    });
    // Then
    await expect(result).rejects.toBeInstanceOf(VscreenScopeError);
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(["account", "backend"])(
    "invalidates a captured scoped client when %s switches",
    async (change) => {
      // Given
      const client = new ApiClient(scope.backendIdentity);
      setApiInstance(client);
      const scoped = client.vscreen(scope);
      const fetch = vi.fn<typeof globalThis.fetch>();
      vi.stubGlobal("fetch", fetch);
      if (change === "account")
        client.vscreen({ ...scope, accountId: "user-2" });
      else setApiInstance(new ApiClient("https://other.test"));
      // When
      const result = scoped.getState();
      // Then
      await expect(result).rejects.toBeInstanceOf(VscreenScopeError);
      expect(fetch).not.toHaveBeenCalled();
    },
  );

  it("keeps legacy snake_case session fields when optional viewer grants are absent", () => {
    // Given / When
    const session = MirrorSessionResponseSchema.parse(sessionWire);
    // Then
    expect(session.runtime_id).toBe(scope.runtimeId);
    expect(session.viewer_grant).toBeUndefined();
  });

  it("maps an optional legacy viewer grant and omits malformed grants without breaking rendering", () => {
    // Given / When
    const valid = MirrorSessionResponseSchema.parse({
      ...sessionWire,
      viewer_grant: grantWire,
    });
    const malformed = MirrorSessionResponseSchema.parse({
      ...sessionWire,
      viewer_grant: { grant_id: "bad" },
    });
    // Then
    expect(valid.viewer_grant?.sourceGeneration).toBe("capture-1");
    expect(malformed.viewer_grant).toBeUndefined();
  });
});
