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
const stream = { getTracks: () => [{ stop: stopTrack }] };
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
  getUserMedia.mockResolvedValue(stream);
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("chat dictation lifecycle", () => {
  it("starts on activation, updates snapshots and finishes only after final correction", async () => {
    const view = setup();
    await act(() => view.result.current.start());
    expect(view.result.current.phase).toBe("recording");
    act(() => { view.partial("hel"); view.partial("hello"); });
    expect(view.draft.update.mock.calls).toEqual([["hel"], ["hello"]]);
    act(() => view.result.current.stop());
    expect(view.result.current.phase).toBe("transcribing");
    expect(view.result.current.isActive()).toBe(true);
    expect(stopTrack).toHaveBeenCalled();
    expect(view.draft.finish).not.toHaveBeenCalled();
    await act(async () => view.pending.resolve("Hello!"));
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
    await act(() => view.result.current.start());
    expect(view.result.current.error).toBe(false);
    await act(async () => view.pending.resolve("  "));
    expect(view.draft.cancel).toHaveBeenCalledTimes(2);
    expect(view.draft.finish).not.toHaveBeenCalled();
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

  it("Escape cancels and recording automatically stops at one minute", async () => {
    vi.useFakeTimers();
    const view = setup();
    await act(() => view.result.current.start());
    act(() => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); });
    expect(view.draft.cancel).toHaveBeenCalledOnce();
    await act(() => view.result.current.start());
    act(() => vi.advanceTimersByTime(60_000));
    expect(view.result.current.phase).toBe("transcribing");
    act(() => vi.advanceTimersByTime(120_000));
    expect(view.result.current.phase).toBe("idle");
    expect(view.result.current.error).toBe(true);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
