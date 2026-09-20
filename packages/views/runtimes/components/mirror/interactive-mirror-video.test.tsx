// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InteractiveMirrorVideo } from "./interactive-mirror-video";

const source = {
  resource: {
    backendIdentity: "https://fixture.invalid",
    accountId: "user-1",
    workspaceId: "workspace-1",
    runtimeId: "runtime-1",
    uid: 1,
  },
  source: { kind: "physical" as const, sourceId: "display-1" },
  nativeEpoch: "native-1",
  generation: "generation-1",
  primary: true,
  name: "Display",
  width: 1600,
  height: 900,
  logicalWidth: 1600,
  logicalHeight: 900,
  scale: 1,
  x: 0,
  y: 0,
  geometryRevision: 1,
  displayId: 1,
};

vi.mock("./mirror-video", () => ({
  MirrorVideo: () => null,
}));

describe("InteractiveMirrorVideo pointer gestures", () => {
  beforeEach(() => {
    vi.stubGlobal("MediaStream", class {});
    Object.defineProperties(Element.prototype, {
      setPointerCapture: { configurable: true, writable: true, value: vi.fn() },
      releasePointerCapture: { configurable: true, writable: true, value: vi.fn() },
      hasPointerCapture: { configurable: true, writable: true, value: vi.fn(() => true) },
    });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      x: 0,
      y: 0,
      left: 0,
      top: 0,
      width: 160,
      height: 90,
      right: 160,
      bottom: 90,
      toJSON: () => ({}),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("keeps one captured mouse gesture and flushes its final move before release", () => {
    const sendInput = vi.fn();
    const { container } = render(
      <InteractiveMirrorVideo
        stream={new MediaStream()}
        label="Remote display"
        onFrame={vi.fn()}
        source={source}
        active
        sendInput={sendInput}
      />,
    );
    const surface = container.querySelector('[role="application"]');
    if (!(surface instanceof HTMLElement)) throw new Error("Surface missing");
    expect(surface).toHaveClass("touch-none", "select-none");

    fireEvent.pointerDown(surface, {
      pointerId: 1,
      pointerType: "mouse",
      button: 0,
      buttons: 1,
      clientX: 80,
      clientY: 45,
    });
    fireEvent.pointerDown(surface, {
      pointerId: 2,
      pointerType: "mouse",
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 20,
    });
    fireEvent.pointerMove(surface, {
      pointerId: 2,
      pointerType: "mouse",
      buttons: 1,
      clientX: 24,
      clientY: 24,
    });
    fireEvent.pointerMove(surface, {
      pointerId: 1,
      pointerType: "mouse",
      buttons: 1,
      clientX: 160,
      clientY: 90,
    });
    fireEvent.pointerUp(surface, {
      pointerId: 2,
      pointerType: "mouse",
      button: 0,
      buttons: 0,
      clientX: 24,
      clientY: 24,
    });
    fireEvent.pointerUp(surface, {
      pointerId: 1,
      pointerType: "mouse",
      button: 0,
      buttons: 0,
      clientX: 160,
      clientY: 90,
    });

    expect(sendInput.mock.calls).toEqual([
      [
        expect.objectContaining({
          kind: "pointer:down",
          pointer: expect.objectContaining({ button: "left", x: 800, y: 450 }),
        }),
      ],
      [
        expect.objectContaining({
          kind: "pointer:move",
          pointer: expect.objectContaining({ button: "left", x: 1600, y: 900 }),
        }),
      ],
      [
        expect.objectContaining({
          kind: "pointer:up",
          pointer: expect.objectContaining({ button: "left", x: 1600, y: 900 }),
        }),
      ],
    ]);
    expect(sendInput.mock.calls[0]?.[0].gestureId).toBe(
      sendInput.mock.calls[1]?.[0].gestureId,
    );
    expect(sendInput.mock.calls[0]?.[0].gestureId).toBe(
      sendInput.mock.calls[2]?.[0].gestureId,
    );
  });
});
