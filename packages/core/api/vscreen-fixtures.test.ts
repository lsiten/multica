// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseVscreenState } from "./vscreen-state";
import { VscreenSourceDescriptorSchema } from "./vscreen-schemas";
import { scope, sourceWire, stateWire } from "./vscreen-fixtures";

describe("Vscreen state boundary", () => {
  it("provides a valid source fixture with the authoritative geometry revision", () => {
    const source = VscreenSourceDescriptorSchema.parse(sourceWire);

    expect(source.geometryRevision).toBe(stateWire.state.geometry_revision);
  });

  it("maps valid wire identity and authoritative state when the response matches the scope", () => {
    // Given
    const raw = stateWire;
    // When
    const parsed = parseVscreenState(raw, scope);
    // Then
    expect(parsed?.state).toMatchObject({
      runtimeId: scope.runtimeId,
      stateRevision: 3,
      activeTaskId: "task-1",
      permissions: { screenRecording: "granted" },
    });
  });
});
