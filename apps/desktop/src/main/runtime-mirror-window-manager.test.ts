// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
vi.mock("electron", () => ({}));
import { mirrorWindowBounds } from "./runtime-mirror-window-manager";
const workArea = { x: 1920, y: 28, width: 1440, height: 872 };
describe("Floating mirror geometry", () => {
  it("starts at the upper right of the actual work area", () => {
    // Given / When
    const bounds = mirrorWindowBounds(workArea);
    // Then
    expect(bounds).toMatchObject({ x: 2984, y: 44, width: 360 });
    expect(bounds.height).toBeGreaterThan((360 * 9) / 16);
  });
  it.each([
    [20, 240],
    [2000, 720],
  ])("bounds requested width %s to %s", (requested, expected) => {
    // Given / When
    const bounds = mirrorWindowBounds(workArea, requested);
    // Then
    expect(bounds.width).toBe(expected);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(
      workArea.x + workArea.width,
    );
  });
});
