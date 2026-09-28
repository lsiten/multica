import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { recordDictation } from "./chat-dictation-recorder";

const state = { data: new Blob(["synthetic audio"]) };
class Recorder {
  static isTypeSupported() { return true; }
  state = "inactive";
  mimeType = "audio/webm";
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  start() { this.state = "recording"; }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: state.data });
    this.onstop?.();
  }
}

beforeEach(() => {
  state.data = new Blob(["synthetic audio"]);
  vi.stubGlobal("MediaRecorder", Recorder);
  vi.stubGlobal("MediaStream", class {});
});
afterEach(() => vi.unstubAllGlobals());

function recording() {
  const controller = new AbortController();
  const transcribe = vi.fn(async () => "Transcript");
  // Recorder does not access the stream; this test owns only batch finalization.
  const stream = new MediaStream();
  return { controller, transcribe, ...recordDictation(stream, controller.signal, transcribe) };
}

describe("dictation batch finalization", () => {
  it("waits for stop before uploading one recording", async () => {
    const capture = recording();
    expect(capture.transcribe).not.toHaveBeenCalled();
    capture.stop();
    await expect(capture.promise).resolves.toBe("Transcript");
    capture.stop();
    expect(capture.transcribe).toHaveBeenCalledOnce();
  });

  it.each([0, 512 * 1024 + 1])("rejects a %i-byte recording before upload", async (size) => {
    state.data = new Blob([new Uint8Array(size)]);
    const capture = recording();
    capture.stop();
    await expect(capture.promise).rejects.toThrow("Empty or oversized recording");
    expect(capture.transcribe).not.toHaveBeenCalled();
  });

  it("cancellation rejects and never uploads captured audio", async () => {
    const capture = recording();
    capture.controller.abort();
    await expect(capture.promise).rejects.toMatchObject({ name: "AbortError" });
    expect(capture.transcribe).not.toHaveBeenCalled();
  });
});
