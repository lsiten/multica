// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { LOCAL_VOICE_CHANNEL } from "../shared/local-voice";
import type { VoiceBridgeTranscribe } from "./voice-bridge";
import { setupLocalVoice } from "./local-voice";

const electron = vi.hoisted(() => ({
  handlers: new Map<string, (...args: unknown[]) => unknown>(),
  workerListeners: new Map<string, (message: unknown) => void>(),
  bridge: vi.fn<(path: string, transcribe: VoiceBridgeTranscribe) => Promise<() => void>>(),
  appListeners: new Map<string, () => void>(),
  mainFrame: {},
  postMessage: vi.fn<(message: unknown) => void>(),
  kill: vi.fn(),
  preferredLanguages: vi.fn<() => string[]>().mockReturnValue(["zh-Hans-CN"]),
}));
vi.mock("electron", () => ({
  app: {
    getLocale: () => "zh-CN",
    getPreferredSystemLanguages: electron.preferredLanguages,
    getPath: () => "/unused-voice-test",
    on: vi.fn(),
    once: (event: string, listener: () => void) => electron.appListeners.set(event, listener),
  },
  BrowserWindow: {
    getAllWindows: () => [],
    fromWebContents: () => ({ webContents: { mainFrame: electron.mainFrame } }),
  },
  ipcMain: {
    handle: (channel: string, handler: (...args: unknown[]) => unknown) => electron.handlers.set(channel, handler),
    on: vi.fn(),
  },
  net: { fetch: vi.fn() },
  utilityProcess: {
    fork: () => ({
      on: (event: string, listener: (message: unknown) => void) => electron.workerListeners.set(event, listener),
      postMessage: electron.postMessage,
      kill: electron.kill,
    }),
  },
}));
vi.mock("./voice-bridge", () => ({ startVoiceBridge: electron.bridge.mockResolvedValue(() => {}) }));
vi.mock("./voice-decoder", () => ({ decodeVoiceAudio: async () => new Float32Array([0.1]) }));
vi.mock("./voice-download", () => ({ downloadVoiceAssets: async () => undefined }));

afterEach(() => {
  electron.appListeners.get("before-quit")?.();
  electron.handlers.clear();
  electron.workerListeners.clear();
  electron.appListeners.clear();
  vi.clearAllMocks();
});

async function fixture() {
  await setupLocalVoice();
  await Promise.resolve();
  electron.workerListeners.get("message")?.({ type: "ready" });
  const request = electron.handlers.get(LOCAL_VOICE_CHANNEL.transcribe);
  if (!request) throw new Error("Transcription handler missing");
  const event = { sender: { id: 1 }, senderFrame: electron.mainFrame };
  return { request, event, samples: new Float32Array([0.1]) };
}

it.each([undefined, null, "", "zh-Hans", "auto", "de", 1, ["zh"], { language: "zh" }])(
  "rejects unsupported IPC language %j before inference",
  async (language) => {
    const f = await fixture();

    expect(() => f.request(f.event, "recording", f.samples, language)).toThrow("Invalid voice recording");

    expect(electron.postMessage).not.toHaveBeenCalled();
  },
);

it("forwards the selected language to the worker and returns its transcript", async () => {
  const f = await fixture();

  const result = f.request(f.event, "recording", f.samples, "zh");
  electron.workerListeners.get("message")?.({ type: "result", id: "recording", text: "中文" });

  expect(electron.postMessage).toHaveBeenCalledWith({ id: "recording", samples: f.samples, language: "zh" });
  await expect(result).resolves.toBe("中文");
});


it.each([
  { preferred: ["zh-Hans-CN"], requested: undefined, language: "zh" },
  { preferred: ["ja-JP"], requested: undefined, language: "ja" },
  { preferred: ["de-DE"], requested: undefined, language: "en" },
  { preferred: [], requested: undefined, language: "zh" },
  { preferred: ["zh-Hans-CN"], requested: "en", language: "en" },
  { preferred: ["en-US"], requested: "zh-Hans", language: "zh" },
  { preferred: ["en-US"], requested: "zh-Hant", language: "zh" },
  { preferred: ["en-US"], requested: "es", language: "es" },
  { preferred: ["en-US"], requested: "fr", language: "fr" },
  { preferred: ["en-US"], requested: "ja", language: "ja" },
  { preferred: ["en-US"], requested: "ko", language: "ko" },
  { preferred: ["en-US"], requested: "ru", language: "ru" },
])("uses requested UI locale $requested with legacy host fallback", async ({ preferred, requested, language }) => {
  electron.preferredLanguages.mockReturnValue(preferred);
  await fixture();
  const bridge = electron.bridge.mock.calls.at(-1)?.[1];
  if (!bridge) throw new Error("Voice bridge missing");
  const result = bridge({ bytes: Buffer.from("audio"), mimeType: "audio/mp4", language: requested }, new AbortController().signal);
  await vi.waitFor(() => expect(electron.postMessage).toHaveBeenCalled());
  const message = electron.postMessage.mock.calls[0]?.[0];
  if (!message || typeof message !== "object" || !("id" in message)) throw new Error("Worker request missing");
  electron.workerListeners.get("message")?.({ type: "result", id: message.id, text: "中文" });
  await expect(result).resolves.toBe("中文");
  expect(message).toMatchObject({ language });
});

it("keeps desktop voice working when the runtime bridge cannot start", async () => {
  electron.bridge.mockRejectedValueOnce(new Error("bind failed"));
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  try {
    const f = await fixture();
    const result = f.request(f.event, "recording", f.samples, "zh");
    electron.workerListeners.get("message")?.({ type: "result", id: "recording", text: "中文" });
    await expect(result).resolves.toBe("中文");
  } finally { warn.mockRestore(); }
});

it.each([
  { reason: "no_signal", code: "no_speech" },
  { reason: "unreliable_transcript", code: "unreliable_transcript" },
])("retains worker rejection $reason for Web callers without transcript text", async ({ reason, code }) => {
  await fixture();
  const bridge = electron.bridge.mock.calls.at(-1)?.[1];
  if (!bridge) throw new Error("Voice bridge missing");
  const result = bridge({ bytes: Buffer.from("audio"), mimeType: "audio/mp4", language: "en" }, new AbortController().signal);
  const rejected = expect(result).rejects.toMatchObject({ code });
  await vi.waitFor(() => expect(electron.postMessage).toHaveBeenCalled());
  const message = electron.postMessage.mock.calls[0]?.[0];
  if (!message || typeof message !== "object" || !("id" in message)) throw new Error("Worker request missing");
  electron.workerListeners.get("message")?.({ type: "error", id: message.id, reason });
  await rejected;
});
