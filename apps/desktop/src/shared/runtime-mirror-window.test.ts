// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  parseRuntimeMirrorWindowRequest,
  readRuntimeMirrorWindowContext,
  RUNTIME_MIRROR_ARGUMENT,
  runtimeMirrorWindowKey,
} from "./runtime-mirror-window";
const scope = {
  backendIdentity: "https://fixture.test",
  accountId: "user-1",
  workspaceId: "ws-1",
  runtimeId: "runtime-1",
};
describe("Floating mirror identity", () => {
  it.each(["accountId", "workspaceId", "runtimeId"])(
    "rejects path-shaped %s before it reaches native window creation",
    (field) => {
      // Given / When
      const result = parseRuntimeMirrorWindowRequest({
        scope: { ...scope, [field]: "../foreign" },
        title: "Fixture",
      });
      // Then
      expect(result).toBeNull();
    },
  );
  it.each(["accountId", "workspaceId", "runtimeId", "backendIdentity"])(
    "keeps native window identities separate when %s differs",
    (field) => {
      // Given / When
      const key = runtimeMirrorWindowKey({ ...scope, [field]: "other" });
      // Then
      expect(key).not.toBe(runtimeMirrorWindowKey(scope));
    },
  );
  it("reads only bounded identity metadata from launch arguments", () => {
    // Given
    const argument =
      RUNTIME_MIRROR_ARGUMENT +
      encodeURIComponent(
        JSON.stringify({ scope, title: "Fixture", generation: 3 }),
      );
    // When
    const context = readRuntimeMirrorWindowContext([argument]);
    // Then
    expect(context).toEqual({
      kind: "runtime-mirror",
      scope,
      title: "Fixture",
      generation: 3,
    });
  });
});
