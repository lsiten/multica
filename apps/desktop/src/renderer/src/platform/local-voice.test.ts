// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { createLocalVoiceAdapter } from "./local-voice";

afterEach(() => vi.unstubAllGlobals());

function fixture() {
  let node!: { port: { onmessage?: (event: { data: Float32Array }) => void } };
  const close = vi.fn(async () => undefined);
  vi.stubGlobal("AudioContext", class {
    audioWorklet = { addModule: async () => undefined };
    destination = {};
    createMediaStreamSource() { return { connect: vi.fn(), disconnect: vi.fn() }; }
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
    transcribe: vi.fn(async (_id: string, _samples: Float32Array) => "words"),
    cancel: vi.fn(),
  };
  const stop = vi.fn();
  const adapter = createLocalVoiceAdapter(api);
  const controller = new AbortController();
  const partial = vi.fn();
  const session = adapter.transcribeStream!({ getTracks: () => [{ stop }] }, controller.signal, partial);
  return { api, controller, session, partial, close, emit: (seconds: number) => node.port.onmessage?.({ data: new Float32Array(seconds * 16000).fill(0.1) }) };
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
