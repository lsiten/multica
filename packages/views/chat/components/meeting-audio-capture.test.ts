// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMeetingAudioCapture } from "./meeting-audio-capture";

type Processor = { onaudioprocess?: (event: { inputBuffer: { getChannelData: (channel: number) => Float32Array } }) => void; connect: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn> };

let processor: Processor;
let closeSpy = vi.fn<() => void>();
let close: () => void = closeSpy;

class FakeAudioContext {
  sampleRate = 8_000;
  destination = {};
  createMediaStreamSource() { return { connect: vi.fn(), disconnect: vi.fn() }; }
  createScriptProcessor() {
    processor = { onaudioprocess: undefined, connect: vi.fn(), disconnect: vi.fn() };
    return processor;
  }
  createGain() { return { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() }; }
  resume() { return Promise.resolve(); }
  close() { close(); return Promise.resolve(); }
}

function audio(samples: number[]) {
  processor.onaudioprocess?.({ inputBuffer: { getChannelData: () => Float32Array.from(samples) } });
}

afterEach(() => vi.unstubAllGlobals());

describe("meeting audio capture", () => {
  it("emits independent ordered WAV chunks and flushes the tail on stop", async () => {
    closeSpy = vi.fn<() => void>();
    close = closeSpy;
    vi.stubGlobal("AudioContext", FakeAudioContext);
    const chunks: Blob[] = [];
    const capture = createMeetingAudioCapture({ stream: {} as MediaStream, chunkDurationMs: 1, onChunk: (chunk) => chunks.push(chunk), onError: vi.fn() });
    capture.start();
    audio([0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1]);
    audio([0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9]);
    audio([0.3, 0.3]);
    await capture.stop();
    expect(chunks).toHaveLength(3);
    expect(chunks.map((chunk) => chunk.type)).toEqual(["audio/wav", "audio/wav", "audio/wav"]);
    const first = chunks.at(0);
    const second = chunks.at(1);
    const third = chunks.at(2);
    if (!first || !second || !third) throw new Error("expected three audio chunks");
    expect(new Uint8Array(await first.arrayBuffer())).not.toEqual(new Uint8Array(await second.arrayBuffer()));
    expect(new Uint8Array(await second.arrayBuffer())).not.toEqual(new Uint8Array(await third.arrayBuffer()));
    expect(closeSpy).toHaveBeenCalledOnce();
  });

  it("discards buffered audio when cancelled", async () => {
    closeSpy = vi.fn<() => void>();
    close = closeSpy;
    vi.stubGlobal("AudioContext", FakeAudioContext);
    const onChunk = vi.fn();
    const capture = createMeetingAudioCapture({ stream: {} as MediaStream, chunkDurationMs: 1, onChunk, onError: vi.fn() });
    capture.start();
    audio([0.5, 0.5, 0.5]);
    capture.cancel();
    await capture.stop();
    expect(onChunk).not.toHaveBeenCalled();
    expect(closeSpy).toHaveBeenCalledOnce();
  });
});
