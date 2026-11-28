import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatDictation } from "./use-chat-dictation";

function deferred<T>() {
  let resolve: (value: T) => void = () => { throw new Error("Promise not ready"); };
  let reject: (error: Error) => void = () => { throw new Error("Promise not ready"); };
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const stopTrack = vi.fn();
const track = { stop: stopTrack, enabled: true };
const stream = { getTracks: () => [track] };
const getUserMedia = vi.fn(async () => stream);

function setup() {
  const pending = deferred<string>();
  const draft = { update: vi.fn(), finish: vi.fn(), cancel: vi.fn() };
  const onActiveChange = vi.fn();
  const stop = vi.fn();
  let partial: (text: string) => void = () => undefined;
  let signal: AbortSignal | undefined;
  const options = {
    enabled: true,
    onBegin: () => draft,
    onActiveChange,
    onVoice: vi.fn(async () => "batch text"),
    onRealtimeVoice: (_stream: unknown, request: AbortSignal, onPartial: (text: string) => void) => {
      signal = request;
      partial = onPartial;
      return { stop, promise: pending.promise };
    },
  };
  const hook = renderHook((props) => useChatDictation(props), { initialProps: options });
  return { ...hook, options, draft, pending, stop, onActiveChange, partial: (text: string) => partial(text), signal: () => signal };
}

beforeEach(() => {
  vi.clearAllMocks();
  track.enabled = true;
  getUserMedia.mockResolvedValue(stream);
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("chat dictation lifecycle", () => {
  it("unlocks the meter during activation and retains only real recent levels", async () => {
    vi.useFakeTimers();
    let inGesture = true;
    let running = false;
    let amplitude = 0;
    let meterClosed = false;
    class GestureAudio {
      get state() { return running ? "running" : "suspended"; }
      destination = {};
      resume() { running = inGesture; return Promise.resolve(); }
      close() { meterClosed = true; return Promise.resolve(); }
      createMediaStreamSource() { return { connect() {}, disconnect() {} }; }
      createGain() { return { gain: { value: 1 }, connect() {}, disconnect() {} }; }
      createAnalyser() {
        return { fftSize: 1024, connect() {}, disconnect() {}, getFloatTimeDomainData(samples: Float32Array) { samples.forEach((_sample, index) => { samples[index] = index % 2 ? amplitude : -amplitude; }); } };
      }
    }
    vi.stubGlobal("AudioContext", GestureAudio);
    getUserMedia.mockImplementationOnce(async () => { inGesture = false; return stream; });
    const view = setup();
    await act(() => view.result.current.start());
    act(() => vi.advanceTimersByTime(80));
    expect(view.result.current.level).toBe(0);
    amplitude = 0.1;
    act(() => vi.advanceTimersByTime(80));
    expect(view.result.current.levels).toHaveLength(2);
    expect(view.result.current.levels[0]).toBe(0);
    expect(view.result.current.levels[1]).toBeGreaterThan(0.8);
    act(() => view.result.current.togglePause());
    act(() => vi.advanceTimersByTime(160));
    expect(view.result.current.levels).toEqual([]);
    act(() => view.result.current.cancel());
    expect(meterClosed).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("transcribes the complete recording when MediaRecorder flushes after stop", async () => {
    let trackStopped = false;
    const recordingTrack = {
      enabled: true,
      stop: vi.fn(() => { trackStopped = true; }),
    };
    const recordingStream = { getTracks: () => [recordingTrack] };
    getUserMedia.mockResolvedValueOnce(recordingStream);
    class AsyncRecorder {
      static isTypeSupported() { return true; }
      state = "inactive";
      mimeType = "audio/webm";
      ondataavailable?: (event: { data: Blob }) => void;
      onstop?: () => void;
      start() { this.state = "recording"; }
      stop() {
        this.state = "inactive";
        queueMicrotask(() => {
          this.ondataavailable?.({ data: new Blob([trackStopped ? "truncated" : "complete"]) });
          this.onstop?.();
        });
      }
    }
    vi.stubGlobal("MediaRecorder", AsyncRecorder);
    const draft = { update: vi.fn(), finish: vi.fn(), cancel: vi.fn() };
    const view = renderHook(() => useChatDictation({
      enabled: true,
      onBegin: () => draft,
      onActiveChange: vi.fn(),
      onVoice: async (audio: Blob) => audio.text(),
    }));

    await act(() => view.result.current.start());
    act(() => view.result.current.stop());
    await act(async () => {});

    expect(draft.update).toHaveBeenLastCalledWith("complete");
    expect(draft.finish).toHaveBeenCalledOnce();
    expect(trackStopped).toBe(true);
  });

  it("starts on activation, updates snapshots and finishes only after final correction", async () => {
    const view = setup();
    await act(() => view.result.current.start());
    expect(view.result.current.phase).toBe("recording");
    act(() => { view.partial("hel"); view.partial("hello"); });
    expect(view.draft.update.mock.calls).toEqual([["hel"], ["hello"]]);
    act(() => view.result.current.stop());
    expect(view.result.current.phase).toBe("transcribing");
    expect(view.result.current.isActive()).toBe(true);
    expect(view.draft.finish).not.toHaveBeenCalled();
    await act(async () => view.pending.resolve("Hello!"));
    expect(stopTrack).toHaveBeenCalled();
    expect(view.draft.update).toHaveBeenLastCalledWith("Hello!");
    expect(view.draft.finish).toHaveBeenCalledOnce();
    expect(view.draft.cancel).not.toHaveBeenCalled();
    expect(view.result.current.isActive()).toBe(false);
    expect(view.signal()?.aborted).toBe(true);
  });

  it("cancels while permission is pending and releases the late microphone", async () => {
    const permission = deferred<typeof stream>();
    getUserMedia.mockReturnValueOnce(permission.promise);
    const view = setup();
    let start: Promise<void> | undefined;
    act(() => { start = view.result.current.start(); });
    expect(view.result.current.phase).toBe("requesting");
    act(() => view.result.current.cancel());
    await act(async () => { permission.resolve(stream); await start; });
    expect(stopTrack).toHaveBeenCalledOnce();
    expect(view.draft.cancel).toHaveBeenCalledOnce();
    expect(view.result.current.phase).toBe("idle");
  });

  it("ignores partial and final callbacks after cancellation", async () => {
    const view = setup();
    await act(() => view.result.current.start());
    act(() => { view.partial("visible"); view.result.current.cancel(); view.partial("late"); });
    await act(async () => view.pending.resolve("late final"));
    expect(view.draft.update.mock.calls).toEqual([["visible"]]);
    expect(view.draft.cancel).toHaveBeenCalledOnce();
    expect(view.draft.finish).not.toHaveBeenCalled();
    expect(view.signal()?.aborted).toBe(true);
  });

  it("retains readable partial text on final transcription failure", async () => {
    const view = setup();
    await act(() => view.result.current.start());
    act(() => { view.partial("already recognized"); view.result.current.stop(); });
    await act(async () => view.pending.reject(new Error("decoder failed")));
    expect(view.draft.finish).toHaveBeenCalledOnce();
    expect(view.draft.cancel).not.toHaveBeenCalled();
    expect(view.result.current.error).toBe(true);
    expect(view.result.current.isActive()).toBe(false);
  });

  it("rolls back an empty result and microphone denial, allowing retry", async () => {
    getUserMedia.mockRejectedValueOnce(new DOMException("Denied", "NotAllowedError"));
    const view = setup();
    await act(() => view.result.current.start());
    expect(view.draft.cancel).toHaveBeenCalledOnce();
    expect(view.result.current.error).toBe(true);
    expect(view.result.current.errorKind).toBe("permission");
    await act(() => view.result.current.start());
    expect(view.result.current.error).toBe(false);
    await act(async () => view.pending.resolve("  "));
    expect(view.draft.cancel).toHaveBeenCalledTimes(2);
    expect(view.draft.finish).not.toHaveBeenCalled();
    expect(view.result.current.errorKind).toBe("empty");
  });

  it("cancels and aborts capture when disabled or unmounted", async () => {
    const view = setup();
    await act(() => view.result.current.start());
    view.rerender({ ...view.options, enabled: false });
    expect(view.draft.cancel).toHaveBeenCalledOnce();
    expect(view.signal()?.aborted).toBe(true);
    expect(view.result.current.isActive()).toBe(false);
    view.rerender(view.options);
    await act(() => view.result.current.start());
    view.unmount();
    expect(view.draft.cancel).toHaveBeenCalledTimes(2);
    expect(view.signal()?.aborted).toBe(true);
  });

  it("automatically stops at one minute and releases timed-out transcription", async () => {
    vi.useFakeTimers();
    const view = setup();
    await act(() => view.result.current.start());
    act(() => vi.advanceTimersByTime(60_000));
    expect(view.result.current.phase).toBe("transcribing");
    act(() => vi.advanceTimersByTime(120_000));
    expect(view.result.current.phase).toBe("idle");
    expect(view.result.current.error).toBe(true);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("pauses microphone input and excludes paused time from the recording limit", async () => {
    vi.useFakeTimers();
    const view = setup();
    await act(() => view.result.current.start());
    act(() => vi.advanceTimersByTime(20_000));
    act(() => view.result.current.togglePause());
    expect(track.enabled).toBe(false);
    expect(view.result.current.phase).toBe("paused");
    act(() => vi.advanceTimersByTime(90_000));
    expect(view.result.current.seconds).toBe(20);
    expect(view.stop).not.toHaveBeenCalled();
    act(() => view.result.current.togglePause());
    expect(track.enabled).toBe(true);
    act(() => vi.advanceTimersByTime(39_000));
    expect(view.result.current.seconds).toBe(59);
    expect(view.result.current.phase).toBe("recording");
    act(() => vi.advanceTimersByTime(1000));
    expect(view.result.current.phase).toBe("transcribing");
  });

  it.each(["recording", "paused", "transcribing"])("retains the final draft when backgrounded during %s", async (phase) => {
    const view = setup();
    await act(() => view.result.current.start());
    act(() => view.partial("Recognized text"));
    if (phase === "paused") act(() => view.result.current.togglePause());
    if (phase === "transcribing") act(() => view.result.current.stop());
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(view.result.current.phase).toBe("transcribing");
    await act(async () => view.pending.resolve("Final preserved text"));
    expect(view.draft.update).toHaveBeenLastCalledWith("Final preserved text");
    expect(view.draft.finish).toHaveBeenCalledOnce();
    expect(view.draft.cancel).not.toHaveBeenCalled();
    vi.restoreAllMocks();
  });
});
