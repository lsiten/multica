import { randomBytes, randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { writeFile, rename, rm } from "node:fs/promises";
import { z } from "zod";

const MAX_AUDIO_BYTES = 512 * 1024;
const AudioRequest = z.object({
  mime_type: z.string().max(128).regex(/^audio\/(webm|ogg|mp4|wav|x-wav)(;.*)?$/),
  audio_base64: z.string().min(1).max(Math.ceil(MAX_AUDIO_BYTES / 3) * 4),
}).strict();
export type VoiceBridgeAudio = { readonly mimeType: string; readonly bytes: Buffer };
export type VoiceBridgeTranscribe = (audio: VoiceBridgeAudio, signal: AbortSignal) => Promise<string>;

export async function startVoiceBridge(descriptorPath: string, transcribe: VoiceBridgeTranscribe) {
  const token = randomBytes(32).toString("hex");
  let active = false;
  const lifetime = new AbortController();
  const server = createServer(async (request, response) => {
    response.setHeader("Cache-Control", "no-store");
    if (request.method !== "POST" || request.url !== "/transcribe" || request.headers.origin || request.headers.authorization !== `Bearer ${token}`) {
      response.writeHead(403).end(); return;
    }
    if (active) { response.writeHead(429).end(); return; }
    active = true;
    const cancelled = new AbortController();
    response.once("close", () => { if (!response.writableFinished) cancelled.abort(); });
    const signal = AbortSignal.any([cancelled.signal, lifetime.signal, AbortSignal.timeout(25_000)]);
    try {
      const chunks: Buffer[] = [];
      let length = 0;
      for await (const chunk of request) {
        const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
        length += buffer.length;
        if (length > 720 * 1024) { response.writeHead(413).end(); return; }
        chunks.push(buffer);
      }
      const parsed = AudioRequest.safeParse(JSON.parse(Buffer.concat(chunks).toString("utf8")));
      if (!parsed.success) { response.writeHead(400).end(); return; }
      const bytes = Buffer.from(parsed.data.audio_base64, "base64");
      if (!bytes.length || bytes.length > MAX_AUDIO_BYTES || bytes.toString("base64") !== parsed.data.audio_base64) {
        response.writeHead(400).end(); return;
      }
      signal.throwIfAborted();
      const text = (await transcribe({ mimeType: parsed.data.mime_type, bytes }, signal)).trim();
      if (!text || Buffer.byteLength(text) > 16 * 1024) { response.writeHead(422).end(); return; }
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({ text }));
    } catch (error) { // Request boundary: malformed audio and inference failures never return text.
      console.warn("[voice-bridge] request failed", { kind: error instanceof Error ? error.name : "unknown", message: error instanceof Error && !(error instanceof SyntaxError) ? error.message.slice(0, 200) : "invalid request", aborted: signal.aborted });
      response.writeHead(error instanceof SyntaxError ? 400 : 503).end();
    } finally { active = false; }
  });
  server.requestTimeout = 25_000;
  server.headersTimeout = 5_000;
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => { server.off("error", reject); resolve(); });
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("Voice bridge address unavailable");
  const temporaryPath = `${descriptorPath}.${randomUUID()}.tmp`;
  try {
    await writeFile(temporaryPath, JSON.stringify({ url: `http://127.0.0.1:${address.port}/transcribe`, token }), { mode: 0o600 });
    await rename(temporaryPath, descriptorPath);
  } catch (error) {
    server.close();
    await rm(temporaryPath, { force: true });
    throw error;
  }
  return () => {
    lifetime.abort();
    server.close();
    server.closeAllConnections();
    // Keep the private descriptor: a stopped endpoint fails closed, and a later
    // desktop process atomically replaces it without racing the previous exit.
  };
}
