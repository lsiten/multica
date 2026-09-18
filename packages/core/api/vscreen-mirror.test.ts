// @vitest-environment node
import { describe, expect, it } from "vitest";
import { envelope, grantWire, scope, sourceWire } from "./vscreen-fixtures";
import {
  parseMirrorViewerGrant,
  parseVscreenMirrorSession,
  parseVscreenVideoMetadata,
} from "./vscreen-mirror";
import { parseVscreenSources } from "./vscreen-state";

function fixtureBinding() {
  const binding = parseVscreenSources(
    { ...envelope, sources: [sourceWire] },
    scope,
  )?.sources[0];
  if (!binding) throw new Error("invalid test fixture");
  return binding;
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
  state: "answered",
  answer: { type: "answer", sdp: "v=0" },
  ice_config: { ice_servers: [], turn_configured: false },
  viewer_grant: grantWire,
};

describe("Vscreen viewer bindings", () => {
  it("maps a renewed grant when it belongs to the exact current viewer and capture generation", () => {
    // Given
    const viewer = {
      sessionId: "session-1",
      viewerId: "viewer-1",
      binding: fixtureBinding(),
    };
    // When
    const result = parseMirrorViewerGrant(grantWire, scope, viewer);
    // Then
    expect(result).toMatchObject({
      grantId: "grant-1",
      sourceGeneration: "capture-1",
      source: { kind: "virtual", sourceId: "display-1" },
    });
  });
  it.each([
    "session_id",
    "workspace_id",
    "runtime_id",
    "user_id",
    "viewer_id",
    "native_epoch",
    "source_generation",
  ])("rejects renewal when %s drifts", (field) => {
    // Given
    const viewer = {
      sessionId: "session-1",
      viewerId: "viewer-1",
      binding: fixtureBinding(),
    };
    // When
    const result = parseMirrorViewerGrant(
      { ...grantWire, [field]: "foreign" },
      scope,
      viewer,
    );
    // Then
    expect(result).toBeNull();
  });
  it("rejects a foreign source when its generation happens to match", () => {
    // Given / When
    const result = parseMirrorViewerGrant(
      { ...grantWire, source: { kind: "physical", source_id: "other" } },
      scope,
      {
        sessionId: "session-1",
        viewerId: "viewer-1",
        binding: fixtureBinding(),
      },
    );
    // Then
    expect(result).toBeNull();
  });
  it("maps optional viewer grants when reading a negotiated session", () => {
    // Given / When
    const result = parseVscreenMirrorSession(sessionWire, scope, {
      sessionId: "session-1",
      viewerId: "viewer-1",
      binding: fixtureBinding(),
    });
    // Then
    expect(result?.viewerGrant?.grantId).toBe("grant-1");
    expect(result?.iceConfig.turnConfigured).toBe(false);
  });
  it("rejects a foreign nested grant when the outer session matches", () => {
    // Given / When
    const result = parseVscreenMirrorSession(
      { ...sessionWire, viewer_grant: { ...grantWire, user_id: "foreign" } },
      scope,
      { viewerId: "viewer-1", binding: fixtureBinding() },
    );
    // Then
    expect(result).toBeNull();
  });
  it("rejects a v2 session when its viewer grant is absent", () => {
    // Given
    const raw = { ...sessionWire, viewer_grant: undefined };
    // When
    const result = parseVscreenMirrorSession(raw, scope, {
      viewerId: "viewer-1",
      binding: fixtureBinding(),
    });
    // Then
    expect(result).toBeNull();
  });
  it("keeps Unix nanosecond PTS diagnostic when it exceeds JS integer precision", () => {
    // Given
    const raw = {
      type: "mirror:video-meta",
      source_binding: sourceWire,
      geometry_revision: 2,
      pts_nanos: 1789382523000000000,
      quality: {
        width: 1600,
        height: 900,
        fps: 30,
        bitrate: 4000000,
        max_level_idc: 40,
      },
    };
    // When
    const result = parseVscreenVideoMetadata(raw, {
      binding: fixtureBinding(),
      geometryRevision: 2,
    });
    // Then
    expect(result?.ptsNanos).toBe(raw.pts_nanos);
    expect(result).not.toHaveProperty("inputProof");
  });
  it("accepts only exact video epoch metadata when geometry or capture generation changes", () => {
    // Given
    const metadata = {
      type: "mirror:video-meta",
      source_binding: sourceWire,
      geometry_revision: 2,
      pts_nanos: 123,
      quality: {
        width: 1600,
        height: 900,
        fps: 30,
        bitrate: 4000000,
        max_level_idc: 40,
      },
    };
    const expected = { binding: fixtureBinding(), geometryRevision: 2 };
    // When
    const results = [
      metadata,
      { ...metadata, geometry_revision: 3 },
      {
        ...metadata,
        source_binding: { ...sourceWire, generation: "capture-2" },
      },
    ].map((raw) => parseVscreenVideoMetadata(raw, expected));
    // Then
    expect(results[0]?.ptsNanos).toBe(123);
    expect(results.slice(1)).toEqual([null, null]);
  });
});
