import { fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MirrorVideo } from "./mirror-video";
beforeEach(() => vi.stubGlobal("MediaStream", class {}));
afterEach(() => vi.unstubAllGlobals());
describe("MirrorVideo first frame", () => {
  it("waits for an actual decoded frame before reporting readiness", () => {
    // Given
    const onFrame = vi.fn();
    const stream = new MediaStream();
    const { container } = render(
      <MirrorVideo
        stream={stream}
        label="Viewer"
        onFrame={onFrame}
        ready={false}
      />,
    );
    const video = container.querySelector("video");
    if (!video) throw new Error("Video element missing");
    // When
    fireEvent.loadedData(video);
    // Then
    expect(onFrame).not.toHaveBeenCalled();
    expect(video).toHaveClass("invisible");
  });
  it("reports readiness when loadeddata has a decoded nonzero frame", () => {
    // Given
    const onFrame = vi.fn();
    const { container } = render(
      <MirrorVideo
        stream={new MediaStream()}
        label="Viewer"
        onFrame={onFrame}
        ready={false}
      />,
    );
    const video = container.querySelector("video");
    if (!video) throw new Error("Video element missing");
    Object.defineProperty(video, "readyState", { value: 2 });
    Object.defineProperty(video, "videoWidth", { value: 640 });
    // When
    fireEvent.loadedData(video);
    // Then
    expect(onFrame).toHaveBeenCalledOnce();
  });
});
