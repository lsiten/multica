// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { LOCAL_VOICE_CHANNEL } from "../shared/local-voice";
import { setupLocalVoice } from "./local-voice";

const electron = vi.hoisted(() => ({
  handlers: new Map<string, (...args: unknown[]) => unknown>(),
  workerListeners: new Map<string, (message: unknown) => void>(),
  appListeners: new Map<string, () => void>(),
  mainFrame: {},
  postMessage: vi.fn<(message: unknown) => void>(),
  kill: vi.fn(),
}));
vi.mock("electron", () => ({
  app: {
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
vi.mock("./voice-download", () => ({ downloadVoiceAssets: async () => undefined }));

afterEach(() => {
  electron.appListeners.get("before-quit")?.();
  electron.handlers.clear();
  electron.workerListeners.clear();
  electron.appListeners.clear();
  vi.clearAllMocks();
});

async function fixture() {
  setupLocalVoice();
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
