import { app, BrowserWindow, ipcMain, net, utilityProcess, type IpcMainInvokeEvent, type UtilityProcess } from "electron";
import { randomUUID } from "node:crypto";
import { rm } from "node:fs/promises";
import { join } from "node:path";
import type { LocalVoiceStatus } from "@multica/core/platform";
import { LOCAL_VOICE_CHANNEL, mapVoiceLanguage, validVoiceLanguage, validVoiceSamples, type LocalVoiceLanguage } from "../shared/local-voice";
import { VOICE_ASSETS, VOICE_BASE_URL, VOICE_REVISION } from "./voice-assets";
import { downloadVoiceAssets } from "./voice-download";
import { startVoiceBridge } from "./voice-bridge";
import { decodeVoiceAudio } from "./voice-decoder";
import { VoiceTranscriptionError } from "./voice-error";

export async function setupLocalVoice(): Promise<void> {
  const root = join(app.getPath("userData"), "voice", VOICE_REVISION);
  const lifetime = new AbortController();
  let status: LocalVoiceStatus = { phase: "downloading", percent: 0 };
  let preparing = false;
  let worker: UtilityProcess | undefined;
  let loadingTimer: ReturnType<typeof setTimeout> | undefined;
  let pending: { id: string; sender: number | null; resolve: (text: string) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> } | undefined;
  function publish(next: LocalVoiceStatus) {
    if (JSON.stringify(next) === JSON.stringify(status)) return;
    status = next;
    for (const window of BrowserWindow.getAllWindows()) {
      if (!window.isDestroyed()) window.webContents.send(LOCAL_VOICE_CHANNEL.changed, status);
    }
  }
  function stopWorker() {
    clearTimeout(loadingTimer);
    const previous = worker; worker = undefined; previous?.kill();
    if (pending) { clearTimeout(pending.timer); pending.reject(new Error("Voice transcription interrupted")); pending = undefined; }
  }
  function fail() {
    stopWorker();
    publish({ phase: "failed", percent: "percent" in status ? status.percent : 100 });
  }
  function load() {
    if (lifetime.signal.aborted) return;
    publish({ phase: "loading", percent: 100 });
    try {
      const child = utilityProcess.fork(join(__dirname, "voice-worker.js"), [root], { serviceName: "Offline Whisper", stdio: "ignore" });
      worker = child;
      loadingTimer = setTimeout(fail, 120_000);
      child.on("exit", () => { if (worker === child) fail(); });
      child.on("message", (message: unknown) => {
        if (worker !== child || !message || typeof message !== "object" || !("type" in message)) return;
        switch (message.type) {
          case "ready": clearTimeout(loadingTimer); publish({ phase: "ready" }); break;
          case "failed": fail(); break;
          case "result":
          case "error": {
            if (!pending || !("id" in message) || message.id !== pending.id) return;
            const request = pending; pending = undefined; clearTimeout(request.timer);
            if (message.type === "result" && "text" in message && typeof message.text === "string" && message.text.trim() && message.text.length <= 16 * 1024) request.resolve(message.text);
            else {
              console.warn("[voice] inference rejected", { reason: "reason" in message && typeof message.reason === "string" ? message.reason : "worker_error" });
              const reason = "reason" in message ? message.reason : undefined;
              request.reject(reason === "no_signal" ? new VoiceTranscriptionError("no_speech")
                : reason === "unreliable_transcript" ? new VoiceTranscriptionError("unreliable_transcript")
                  : new Error("Voice transcription failed"));
            }
            break;
          }
          default: break;
        }
      });
    } catch { fail(); }
  }
  async function prepare(options: { readonly forceRefresh?: boolean } = {}) {
    if (preparing || worker || lifetime.signal.aborted) return;
    preparing = true;
    publish({ phase: "downloading", percent: 0 });
    try {
      if (options.forceRefresh) await rm(root, { recursive: true, force: true });
      await downloadVoiceAssets({ root, assets: VOICE_ASSETS, baseURL: VOICE_BASE_URL,
        fetchAsset: (url, options) => net.fetch(url, options), signal: lifetime.signal,
        onProgress: (percent) => publish({ phase: "downloading", percent }) });
      load();
    } catch { if (!lifetime.signal.aborted) fail(); }
    finally { preparing = false; }
  }
  function checkSender(event: IpcMainInvokeEvent) {
    const window = BrowserWindow.fromWebContents(event.sender);
    if (!window || event.senderFrame !== window.webContents.mainFrame) throw new Error("Voice request requires an application window");
  }
  ipcMain.handle(LOCAL_VOICE_CHANNEL.status, (event) => { checkSender(event); return status; });
  ipcMain.handle(LOCAL_VOICE_CHANNEL.retry, (event) => { checkSender(event); void prepare({ forceRefresh: true }); });
  function transcribe(id: string, samples: Float32Array, language: LocalVoiceLanguage, sender: number | null) {
    if (status.phase !== "ready" || !worker || pending) throw new Error("Voice transcription unavailable or busy");
    return new Promise<string>((resolve, reject) => {
      const timer = setTimeout(() => { stopWorker(); load(); }, 120_000);
      pending = { id, sender, resolve, reject, timer };
      worker?.postMessage({ id, samples, language });
    });
  }
  ipcMain.handle(LOCAL_VOICE_CHANNEL.transcribe, (event, id: unknown, samples: unknown, language: unknown) => {
    checkSender(event);
    if (typeof id !== "string" || id.length > 100 || !validVoiceSamples(samples) || !validVoiceLanguage(language)) throw new Error("Invalid voice recording");
    return transcribe(id, samples, language, event.sender.id);
  });
  ipcMain.on(LOCAL_VOICE_CHANNEL.cancel, (event, id: unknown) => {
    if (pending?.id !== id || pending?.sender !== event.sender.id) return;
    stopWorker(); load();
  });
  app.on("web-contents-created", (_event, contents) => {
    contents.once("destroyed", () => { if (pending?.sender === contents.id) { stopWorker(); load(); } });
  });
  const descriptorPath = join(app.getPath("userData"), "voice-bridge.json");
  let stopBridge = () => {};
  try { stopBridge = await startVoiceBridge(descriptorPath, async (audio, signal) => {
    if (status.phase !== "ready" || pending) throw new Error("Voice transcription unavailable or busy");
    const samples = await decodeVoiceAudio(audio, signal);
    signal.throwIfAborted();
    const id = randomUUID();
    const cancel = () => { if (pending?.id === id) { stopWorker(); load(); } };
    signal.addEventListener("abort", cancel, { once: true });
    const locale = audio.language ?? app.getPreferredSystemLanguages()[0] ?? app.getLocale();
    try { return await transcribe(id, samples, mapVoiceLanguage(locale), null); }
    finally { signal.removeEventListener("abort", cancel); }
  });
    process.env.MULTICA_DESKTOP_VOICE_DESCRIPTOR = descriptorPath;
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    console.warn("Desktop voice bridge unavailable:", error.message);
  }
  app.once("before-quit", () => { lifetime.abort(); stopBridge(); stopWorker(); });
  void prepare();
}
