// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MirrorGestures } from "./mirror-gestures";

const sample = (y = 40, type = "touch") => ({
  id: 1, type, clientX: 40, clientY: y, point: { x: 400, y: y * 10 }, button: "left" as const,
});

describe("mirror touch input", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => setTimeout(() => callback(0), 16));
    vi.stubGlobal("cancelAnimationFrame", clearTimeout);
  });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

  it("clicks on touch release without pressing during a potential swipe", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample());
    expect(send).not.toHaveBeenCalled();
    gestures.up(sample(41));
    expect(send.mock.calls.map(([input]) => input.kind)).toEqual(["pointer:down", "pointer:up"]);
  });

  it("scrolls in finger direction and combines moves within a frame without clicking", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample());
    gestures.move(sample(30));
    gestures.move(sample(20));
    expect(send).not.toHaveBeenCalled();
    vi.advanceTimersByTime(16);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ kind: "wheel", pointer: expect.objectContaining({ deltaY: 200 }) }));
    gestures.up(sample(20));
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("holds then drags and flushes the latest move before releasing", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample());
    vi.advanceTimersByTime(350);
    gestures.move(sample(50));
    gestures.move(sample(60));
    gestures.up(sample(60));
    expect(send.mock.calls.map(([input]) => input.kind)).toEqual(["pointer:down", "pointer:move", "pointer:up"]);
    expect(send.mock.calls[1]?.[0].pointer.y).toBe(600);
  });

  it("cancels a pending tap without a remote click", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample());
    gestures.cancel();
    vi.advanceTimersByTime(500);
    expect(send).not.toHaveBeenCalled();
  });

  it("releases a held mouse when focus or control is lost and discards queued movement", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample(40, "mouse"));
    gestures.move(sample(60, "mouse"));
    gestures.cancel();
    vi.advanceTimersByTime(500);
    expect(send.mock.calls.map(([input]) => input.kind)).toEqual(["pointer:down", "pointer:up"]);
  });

  it("ignores secondary pointers without disrupting the first gesture", () => {
    const send = vi.fn();
    const gestures = new MirrorGestures(send);
    gestures.down(sample());
    expect(gestures.down({ ...sample(), id: 2 })).toBe(false);
    gestures.move({ ...sample(10), id: 2 });
    gestures.up({ ...sample(10), id: 2 });
    gestures.up(sample());
    expect(send.mock.calls.map(([input]) => input.kind)).toEqual(["pointer:down", "pointer:up"]);
  });
});
