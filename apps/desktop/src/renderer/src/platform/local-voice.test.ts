// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import type { LocalVoiceLanguage } from "../../../shared/local-voice";
import { createLocalVoiceAdapter } from "./local-voice";

afterEach(() => vi.unstubAllGlobals());

function fixture(getLocale = () => "en") {
  let node!: { port: { onmessage?: (event: { data: Float32Array }) => void } };
  const close = vi.fn(async () => undefined);
  vi.stubGlobal("AudioContext", class {
    audioWorklet = { addModule: async () => undefined };
    destination = {};
    createMediaStreamSource() { return { connect: vi.fn(), disconnect: vi.fn() }; }
    decodeAudioData = async () => ({ duration: 2, length: 32_000, numberOfChannels: 1, getChannelData: () => new Float32Array(32_000).fill(0.1) });
    close = close;
    resume = async () => undefined;
  });
  vi.stubGlobal("AudioWorkletNode", class {
    port: { onmessage?: (event: { data: Float32Array }) => void } = {};
    constructor() { node = { port: this.port }; }
    connect() {}
    disconnect() {}
  });
  const api = {
    getStatus: vi.fn(async () => ({ phase: "ready" as const })),
    onStatus: vi.fn(() => () => undefined),
    retry: vi.fn(async () => undefined),
    transcribe: vi.fn(async (_id: string, _samples: Float32Array, _language: LocalVoiceLanguage) => "words"),
    cancel: vi.fn(),
  };
  const stop = vi.fn();
  const adapter = createLocalVoiceAdapter(api, getLocale);
  const controller = new AbortController();
  const partial = vi.fn();
  const session = adapter.transcribeStream!({ getTracks: () => [{ stop }] }, controller.signal, partial);
  return { api, adapter, controller, session, partial, close, emit: (seconds: number, amplitude = 0.1) => node.port.onmessage?.({ data: new Float32Array(seconds * 16000).fill(amplitude) }) };
}

it("corrects the entire long recording on release", async () => {
  const f = fixture();
  await Promise.resolve();
  f.emit(40);
  await vi.waitFor(() => expect(f.partial).toHaveBeenCalled());
  f.session.stop();
  await expect(f.session.promise).resolves.toBe("words");
  expect(f.api.transcribe.mock.calls.at(-1)?.[1].length).toBe(40 * 16000);
  expect(f.close).toHaveBeenCalled();
});

it("cancels in-flight local inference without starting a final request", async () => {
  const f = fixture();
  f.api.transcribe.mockImplementation(() => new Promise(() => undefined));
  await Promise.resolve();
  f.emit(2);
  f.controller.abort();
  f.session.stop();
  await expect(f.session.promise).rejects.toMatchObject({ name: "AbortError" });
  expect(f.api.cancel).toHaveBeenCalledOnce();
  expect(f.api.transcribe).toHaveBeenCalledOnce();
  expect(f.close).toHaveBeenCalled();
});

it("publishes the whole utterance when dictation exceeds the preview window", async () => {
  const f = fixture();
  f.api.transcribe.mockImplementation(async (_id, samples) =>
    samples.length > 4 * 16000 ? "first sentence and second sentence" : "first sentence",
  );
  await Promise.resolve();
  f.emit(2);
  await vi.waitFor(() => expect(f.partial).toHaveBeenLastCalledWith("first sentence"));

  f.emit(4);

  await vi.waitFor(() => expect(f.partial).toHaveBeenLastCalledWith("first sentence and second sentence"));
  f.session.stop();
  await expect(f.session.promise).resolves.toBe("first sentence and second sentence");
});

it("does not repeat earlier speech while the recent capture is silent", async () => {
  const f = fixture();
  await Promise.resolve();
  f.emit(2);
  await vi.waitFor(() => expect(f.partial).toHaveBeenCalledWith("words"));

  f.emit(4, 0);

  expect(f.api.transcribe).toHaveBeenCalledOnce();
  f.session.stop();
  await expect(f.session.promise).resolves.toBe("words");
});

it.each([
  ["en", "en"], ["zh-Hans", "zh"], ["ja", "ja"], ["ko", "ko"], ["fr", "fr"],
  ["zh-CN", "zh"], ["fr-CA", "fr"], ["", "en"], ["unsupported", "en"],
])("maps application locale %s to Whisper language %s", async (locale, language) => {
  const f = fixture(() => locale);
  await Promise.resolve();

  f.emit(2);
  f.session.stop();
  await f.session.promise;

  expect(f.api.transcribe).toHaveBeenCalledTimes(2);
  expect(f.api.transcribe.mock.calls.map((call) => call[2])).toEqual([language, language]);
});

it("keeps the recording language when the application locale changes", async () => {
  let locale = "zh-Hans";
  const f = fixture(() => locale);
  await Promise.resolve();
  locale = "en";

  f.emit(2);
  f.session.stop();
  await f.session.promise;

  expect(f.api.transcribe.mock.calls.map((call) => call[2])).toEqual(["zh", "zh"]);
});

it("passes the application language for a recorded audio blob", async () => {
  const f = fixture(() => "zh-Hans");
  f.controller.abort();
  await expect(f.session.promise).rejects.toMatchObject({ name: "AbortError" });

  await f.adapter.transcribe(new Blob(), new AbortController().signal);

  expect(f.api.transcribe.mock.calls.map((call) => call[2])).toEqual(["zh"]);
});
