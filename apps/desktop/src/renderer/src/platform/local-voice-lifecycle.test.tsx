import { act, cleanup, render, screen } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useLocalVoice } from "@multica/views/platform";
import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
import type { LocalVoiceAPI, LocalVoiceLanguage } from "../../../shared/local-voice";
import { buildLocalVoiceAdapter, LocalVoiceProvider } from "./local-voice-provider";

function voiceFixture() {
  const listeners = new Set<(status: LocalVoiceStatus) => void>();
  const api = {
    getStatus: vi.fn(async (): Promise<LocalVoiceStatus> => ({ phase: "ready" })),
    onStatus: vi.fn((listener: (status: LocalVoiceStatus) => void) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }),
    retry: vi.fn(async () => {}),
    transcribe: vi.fn(async (_id: string, _samples: Float32Array, _language: LocalVoiceLanguage) => "fixture transcript"),
    cancel: vi.fn(),
  } satisfies LocalVoiceAPI;
  return { api, listeners, emit: (status: LocalVoiceStatus) => listeners.forEach((listener) => listener(status)) };
}

function audioFixture(decode = vi.fn(async () => ({ duration: 1 }))) {
  const close = vi.fn(async () => {});
  class Audio {
    decodeAudioData = decode;
    close = close;
  }
  class OfflineAudio {
    destination = {};
    createBufferSource() { return { buffer: null, connect() {}, start() {} }; }
    async startRendering() { return { getChannelData: () => new Float32Array([0.25]) }; }
  }
  vi.stubGlobal("AudioContext", Audio);
  vi.stubGlobal("OfflineAudioContext", OfflineAudio);
  return { close, decode };
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); delete window.localVoiceAPI; });

describe("offline voice lifecycle", () => {
  it("does no IPC work until subscribed and reconnects after its last consumer leaves", async () => {
    const fixture = voiceFixture();
    const adapter = buildLocalVoiceAdapter(fixture.api, () => "en");
    expect(fixture.listeners.size).toBe(0);
    expect(fixture.api.getStatus).not.toHaveBeenCalled();
    const unsubscribe = adapter.subscribe(vi.fn());
    await Promise.resolve();
    unsubscribe();
    expect(fixture.listeners.size).toBe(0);
    const unsubscribeAgain = adapter.subscribe(vi.fn());
    await Promise.resolve();
    fixture.emit({ phase: "downloading", percent: 42 });
    expect(adapter.getSnapshot()).toEqual({ phase: "downloading", percent: 42 });
    unsubscribeAgain();
    expect(fixture.listeners.size).toBe(0);
  });

  it("delivers status after a StrictMode consumer remount and keeps locale changes on the same adapter", async () => {
    const fixture = voiceFixture();
    window.localVoiceAPI = fixture.api;
    audioFixture();
    const adapters = new Set<LocalVoiceAdapter>();
    function Consumer() {
      const { adapter, status } = useLocalVoice();
      if (adapter) adapters.add(adapter);
      return <output>{status?.phase}</output>;
    }
    const view = render(<StrictMode><LocalVoiceProvider locale="en"><Consumer /></LocalVoiceProvider></StrictMode>);
    await act(async () => {});
    view.rerender(<StrictMode><LocalVoiceProvider locale="en">{null}</LocalVoiceProvider></StrictMode>);
    expect(fixture.listeners.size).toBe(0);
    view.rerender(<StrictMode><LocalVoiceProvider locale="zh-Hans"><Consumer /></LocalVoiceProvider></StrictMode>);
    await act(async () => {});
    act(() => fixture.emit({ phase: "failed", percent: 42 }));
    expect(screen.getByText("failed")).toBeTruthy();
    expect(fixture.listeners.size).toBe(1);
    expect(adapters.size).toBe(1);
    const adapter = adapters.values().next().value;
    if (!adapter) throw new Error("missing adapter");
    await expect(adapter.transcribe(new Blob(["fixture"]), new AbortController().signal)).resolves.toBe("fixture transcript");
    expect(fixture.api.transcribe).toHaveBeenCalledWith(expect.any(String), expect.any(Float32Array), "zh");
  });

  it("closes the decoder when audio decoding rejects", async () => {
    const audio = audioFixture(vi.fn(async () => { throw new Error("invalid audio"); }));
    const fixture = voiceFixture();
    const adapter = buildLocalVoiceAdapter(fixture.api, () => "en");
    await expect(adapter.transcribe(new Blob(["invalid"]), new AbortController().signal)).rejects.toThrow("invalid audio");
    expect(audio.close).toHaveBeenCalledOnce();
    expect(fixture.api.transcribe).not.toHaveBeenCalled();
  });

  it("skips decoding an already aborted recording", async () => {
    const audio = audioFixture();
    const fixture = voiceFixture();
    const controller = new AbortController();
    controller.abort();
    await expect(buildLocalVoiceAdapter(fixture.api, () => "en").transcribe(new Blob(["fixture"]), controller.signal)).resolves.toBe("");
    expect(audio.decode).not.toHaveBeenCalled();
    expect(fixture.api.transcribe).not.toHaveBeenCalled();
  });

  it("discards a late worker transcript after cancellation", async () => {
    audioFixture();
    const fixture = voiceFixture();
    const reply = Promise.withResolvers<string>();
    fixture.api.transcribe.mockReturnValue(reply.promise);
    const controller = new AbortController();
    const pending = buildLocalVoiceAdapter(fixture.api, () => "en").transcribe(new Blob(["fixture"]), controller.signal);
    await vi.waitFor(() => expect(fixture.api.transcribe).toHaveBeenCalledOnce());
    controller.abort();
    reply.resolve("cancelled transcript");
    await expect(pending).resolves.toBe("");
    expect(fixture.api.cancel).toHaveBeenCalledOnce();
  });
});
