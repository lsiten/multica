// @vitest-environment node
import { describe, expect, it } from "vitest";
import { pointToSourceFrame } from "./mirror-coordinates";

const rect = { left: 0, top: 0, width: 160, height: 90 };

describe("pointToSourceFrame", () => {
  it("maps the center of an unletterboxed source frame", () => {
    expect(pointToSourceFrame({ clientX: 80, clientY: 45 }, rect, { width: 1600, height: 900 })).toEqual({
      x: 800,
      y: 450,
    });
  });

  it("clamps points in a four-by-three source's side letterbox", () => {
    const source = { width: 800, height: 600 };

    expect(pointToSourceFrame({ clientX: 0, clientY: 45 }, rect, source)).toEqual({
      x: 0,
      y: 300,
    });
    expect(pointToSourceFrame({ clientX: 159, clientY: 45 }, rect, source)).toEqual({
      x: 800,
      y: 300,
    });
  });

  it("clamps points in a wide source's top and bottom letterbox", () => {
    expect(pointToSourceFrame({ clientX: 40, clientY: 0 }, { left: 0, top: 0, width: 80, height: 60 }, { width: 1600, height: 900 })).toEqual({
      x: 800,
      y: 0,
    });
    expect(pointToSourceFrame({ clientX: 40, clientY: 59 }, { left: 0, top: 0, width: 80, height: 60 }, { width: 1600, height: 900 })).toEqual({
      x: 800,
      y: 900,
    });
  });

  it("clamps negative and outside viewport coordinates", () => {
    const source = { width: 1600, height: 900 };

    expect(pointToSourceFrame({ clientX: -20, clientY: -10 }, rect, source)).toEqual({ x: 0, y: 0 });
    expect(pointToSourceFrame({ clientX: 200, clientY: 120 }, rect, source)).toEqual({ x: 1600, y: 900 });
  });

  it("keeps source coordinates stable when page zoom scales the rect and client coordinates together", () => {
    const source = { width: 1600, height: 900 };
    const zoomedRect = { left: 10, top: 20, width: 320, height: 180 };

    expect(pointToSourceFrame({ clientX: 170, clientY: 110 }, zoomedRect, source)).toEqual({
      x: 800,
      y: 450,
    });
    expect(pointToSourceFrame({ clientX: 330, clientY: 200 }, zoomedRect, source)).toEqual({
      x: 1600,
      y: 900,
    });
  });

  it.each([
    [{ clientX: 0, clientY: 0 }, { ...rect, width: 0 }, { width: 1600, height: 900 }],
    [{ clientX: 0, clientY: 0 }, { ...rect, height: 0 }, { width: 1600, height: 900 }],
    [{ clientX: Number.NaN, clientY: 0 }, rect, { width: 1600, height: 900 }],
    [{ clientX: 0, clientY: Number.POSITIVE_INFINITY }, rect, { width: 1600, height: 900 }],
    [{ clientX: 0, clientY: 0 }, rect, { width: 0, height: 900 }],
    [{ clientX: 0, clientY: 0 }, rect, { width: 1600, height: Number.NaN }],
  ])("rejects invalid geometry", (point, elementRect, source) => {
    expect(pointToSourceFrame(point, elementRect, source)).toBeNull();
  });
});
