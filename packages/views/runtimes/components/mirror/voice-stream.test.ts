// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { startMirrorVoiceStream } from "./voice-stream";

const track = { stop: vi.fn() };
const stream = { getTracks: () => [track] };

class Recorder {
  static latest: Recorder | undefined;
  static isTypeSupported = () => true;
  state = "inactive";
  mimeType = "audio/webm";
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  onerror?: () => void;
  constructor(_stream: unknown, _options?: unknown) { Recorder.latest = this; }
  start() { this.state = "recording"; }
  stop() { this.state = "inactive"; this.onstop?.(); }
  emit(chunk: string) { this.ondataavailable?.({ data: new Blob([chunk]) }); }
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  Recorder.latest = undefined;
});

describe("mirror realtime voice stream", () => {
  it("keeps partial requests cumulative and resolves the final snapshot after stop", async () => {
    vi.stubGlobal("MediaRecorder", Recorder);
    const replies = ["partial", "final"];
    const request = vi.fn(async (recording: Blob) => {
      expect((await recording.text()).length).toBeGreaterThan(0);
      return replies.shift() ?? "final";
    });
    const partial = vi.fn();
    const handle = startMirrorVoiceStream(stream, new AbortController().signal, partial, { request });
    Recorder.latest?.emit("first");
    await Promise.resolve();
    Recorder.latest?.emit("second");
    handle.stop();
    const result = await handle.promise;
    expect(result).toBe("final");
    expect(partial).toHaveBeenCalledWith("partial");
    expect(partial).toHaveBeenLastCalledWith("final");
    expect(request).toHaveBeenCalledTimes(2);
    expect(track.stop).toHaveBeenCalled();
  });

  it("cancels after stop without allowing a late final to update the draft", async () => {
    vi.stubGlobal("MediaRecorder", Recorder);
    let resolve!: (value: string) => void;
    const request = vi.fn(() => new Promise<string>((done) => { resolve = done; }));
    const partial = vi.fn();
    const controller = new AbortController();
    const handle = startMirrorVoiceStream(stream, controller.signal, partial, { request });
    Recorder.latest?.emit("audio");
    handle.stop();
    controller.abort();
    resolve("late final");
    await expect(handle.promise).rejects.toMatchObject({ name: "AbortError" });
    expect(partial).not.toHaveBeenCalled();
  });
});
