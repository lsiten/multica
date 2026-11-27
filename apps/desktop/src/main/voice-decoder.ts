import { BrowserWindow } from "electron";
import { z } from "zod";
import type { VoiceBridgeAudio } from "./voice-bridge";

const Samples = z.array(z.number().finite()).min(1).max(16_000 * 61);

export async function decodeVoiceAudio(audio: VoiceBridgeAudio, signal: AbortSignal): Promise<Float32Array> {
  signal.throwIfAborted();
  const window = new BrowserWindow({ show: false, webPreferences: {
    sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false,
  } });
  const close = () => { if (!window.isDestroyed()) window.destroy(); };
  signal.addEventListener("abort", close, { once: true });
  window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  window.webContents.on("will-navigate", (event) => event.preventDefault());
  let stage = "load";
  try {
    await window.loadURL("data:text/html," + encodeURIComponent('<meta http-equiv="Content-Security-Policy" content="default-src \'none\'"><title>Voice decoder</title>'));
    signal.throwIfAborted();
    stage = "decode";
    const samples: unknown = await window.webContents.executeJavaScript(`(async () => {
      const bytes = Uint8Array.from(atob(${JSON.stringify(audio.bytes.toString("base64"))}), c => c.charCodeAt(0));
      const context = new AudioContext();
      let decoded;
      try { decoded = await context.decodeAudioData(bytes.buffer); } finally { await context.close(); }
      if (!Number.isFinite(decoded.duration) || decoded.duration <= 0 || decoded.duration > 61) throw new Error("Invalid voice duration");
      const offline = new OfflineAudioContext(1, Math.ceil(decoded.duration * 16000), 16000);
      const source = offline.createBufferSource(); source.buffer = decoded;
      source.connect(offline.destination); source.start();
      return Array.from((await offline.startRendering()).getChannelData(0));
    })()`);
    signal.throwIfAborted();
    stage = "validate";
    return Float32Array.from(Samples.parse(samples), (sample) => Math.max(-1, Math.min(1, sample)));
  } catch (error) {
    console.warn("[voice-bridge] audio decode failed", { stage, kind: error instanceof Error ? error.name : "unknown", message: error instanceof Error && !(error instanceof z.ZodError) ? error.message.slice(0, 200) : "invalid decoded samples" });
    throw error;
  } finally {
    signal.removeEventListener("abort", close);
    close();
  }
}
