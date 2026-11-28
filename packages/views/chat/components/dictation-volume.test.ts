// @vitest-environment node
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { prepareDictationVolume } from "./dictation-volume";

let amplitude = 0;
let state = "running";
const disconnect = vi.fn();
const close = vi.fn(async () => { state = "closed"; });
const resume = vi.fn(async () => { state = "running"; });
const connectSource = vi.fn();
class Audio {
  get state() { return state; }
  destination = {};
  close = close;
  resume = resume;
  createMediaStreamSource = connectSource.mockImplementation(() => ({ connect: vi.fn(), disconnect }));
  createGain() { return { gain: { value: 1 }, connect: vi.fn(), disconnect }; }
  createAnalyser() {
    return { fftSize: 1024, connect: vi.fn(), disconnect, getFloatTimeDomainData(samples: Float32Array) {
      samples.forEach((_sample, index) => { samples[index] = index % 2 ? amplitude : -amplitude; });
    } };
  }
}
beforeEach(() => {
  vi.clearAllMocks();
  amplitude = 0;
  state = "running";
  vi.useFakeTimers();
  vi.stubGlobal("AudioContext", Audio);
  vi.stubGlobal("MediaStream", class {});
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

it("reports silence, quiet speech, and louder speech from measured samples", () => {
  const levels: (number | null)[] = [];
  const meter = prepareDictationVolume((level) => levels.push(level));
  meter.connect(new MediaStream());
  vi.advanceTimersByTime(80);
  amplitude = 0.01;
  vi.advanceTimersByTime(80);
  amplitude = 0.1;
  vi.advanceTimersByTime(80);
  expect(levels[0]).toBe(0);
  expect(levels[1]).toBeCloseTo(0.4167, 3);
  expect(levels[2]).toBeCloseTo(0.8333, 3);
  meter.close();
});

it("reports unavailable instead of fabricated silence when processing is suspended", () => {
  const onLevel = vi.fn();
  const meter = prepareDictationVolume(onLevel);
  meter.connect(new MediaStream());
  state = "suspended";
  vi.advanceTimersByTime(80);
  expect(onLevel).toHaveBeenLastCalledWith(null);
  meter.close();
});

it("closes a pending meter and never connects a late microphone", () => {
  const meter = prepareDictationVolume(vi.fn());
  meter.close();
  meter.connect(new MediaStream());
  expect(connectSource).not.toHaveBeenCalled();
  expect(close).toHaveBeenCalledOnce();
  expect(vi.getTimerCount()).toBe(0);
});
