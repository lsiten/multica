import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import type { VscreenSourceDescriptor } from "@multica/core/types";
import { renderWithI18n } from "../../../test/i18n";
import { MirrorSourcePicker, mirrorSourceKey } from "./mirror-source-picker";
const source: VscreenSourceDescriptor = {
  resource: {
    backendIdentity: "https://fixture.test",
    workspaceId: "ws-1",
    runtimeId: "runtime-1",
    uid: 501,
  },
  source: { kind: "virtual", sourceId: "private-native-source-id" },
  nativeEpoch: "native-1",
  generation: "capture-1",
  primary: false,
  name: "A very long display name that cannot fit within a narrow floating viewer",
  width: 640,
  height: 360,
  scale: 1,
};
describe("MirrorSourcePicker full labels", () => {
  it("preserves the full source name as a title when the selected text is constrained", () => {
    // Given / When
    renderWithI18n(
      <MirrorSourcePicker
        catalog={[source]}
        selected={mirrorSourceKey(source)}
        enabled
        compact
        onSelect={vi.fn()}
      />,
    );
    // Then
    expect(screen.getByRole("combobox")).toHaveAttribute(
      "title",
      expect.stringContaining(source.name),
    );
    expect(
      screen.getByRole("option", { name: new RegExp(source.name) }),
    ).toHaveAttribute("title", expect.stringContaining(source.name));
  });
  it("uses an ordinal instead of a native identifier when the source name is blank", () => {
    // Given
    const unnamed = { ...source, name: "   " };
    // When
    renderWithI18n(
      <MirrorSourcePicker
        catalog={[unnamed]}
        selected={mirrorSourceKey(unnamed)}
        enabled
        compact
        onSelect={vi.fn()}
      />,
    );
    // Then
    expect(screen.getByRole("combobox").getAttribute("title")).toMatch(/1$/);
    expect(screen.getByRole("combobox").getAttribute("title")).not.toContain(
      source.source.sourceId,
    );
  });
});
