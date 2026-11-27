// @vitest-environment node
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it } from "vitest";
import { z } from "zod";
import { startVoiceBridge, type VoiceBridgeTranscribe } from "./voice-bridge";

const cleanup: (() => Promise<void> | void)[] = [];
afterEach(async () => { for (const close of cleanup.reverse()) await close(); cleanup.length = 0; });
async function fixture(transcribe: VoiceBridgeTranscribe = async ({ bytes }) => bytes.toString("utf8")) {
  const directory = await mkdtemp(join(tmpdir(), "voice-bridge-"));
  cleanup.push(() => rm(directory, { recursive: true, force: true }));
  const path = join(directory, "voice-bridge.json");
  cleanup.push(await startVoiceBridge(path, transcribe));
  const descriptor = z.object({ url: z.string(), token: z.string() }).parse(JSON.parse(await readFile(path, "utf8")));
  const post = (headers: Record<string, string> = {}, body: unknown = { mime_type: "audio/mp4", audio_base64: Buffer.from("hello").toString("base64") }, signal?: AbortSignal) => fetch(descriptor.url, {
    method: "POST", headers: { Authorization: `Bearer ${descriptor.token}`, ...headers }, body: JSON.stringify(body), signal,
  });
  return { path, post };
}

it("publishes private discovery and transports bounded audio to transcription", async () => {
  const f = await fixture();
  const response = await f.post();
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({ text: "hello" });
  if (process.platform !== "win32") expect((await stat(f.path)).mode & 0o777).toBe(0o600);
});

it.each<Record<string, string>>([{ Authorization: "Bearer wrong" }, { Origin: "https://untrusted.example" }])("rejects unauthenticated or browser requests %j", async (headers) => {
  const f = await fixture(async () => { throw new Error("must not invoke inference"); });
  expect((await f.post(headers)).status).toBe(403);
});

it.each([
  { mime_type: "text/plain", audio_base64: "YQ==" },
  { mime_type: "audio/mp4", audio_base64: "not base64!" },
  { mime_type: "audio/mp4", audio_base64: Buffer.alloc(512 * 1024 + 1).toString("base64") },
])("rejects invalid audio before inference", async (body) => {
  const f = await fixture();
  expect((await f.post({}, body)).status).toBe(400);
});

it("does not turn failed recognition into transcript text", async () => {
  const f = await fixture(async () => { throw new Error("Unreliable voice transcript"); });
  const response = await f.post();
  expect(response.status).toBe(503);
  expect(await response.text()).toBe("");
});

it("cancels inference when caller disconnects and rejects concurrent requests", async () => {
  const started = Promise.withResolvers<void>();
  const cancelled = Promise.withResolvers<void>();
  const f = await fixture(async (_audio, signal) => {
    started.resolve();
    await new Promise<void>((_resolve, reject) => signal.addEventListener("abort", () => { cancelled.resolve(); reject(signal.reason); }, { once: true }));
    return "unreachable";
  });
  const controller = new AbortController();
  const request = f.post({}, undefined, controller.signal).catch(() => undefined);
  await started.promise;
  expect((await f.post()).status).toBe(429);
  controller.abort();
  await cancelled.promise;
  await request;
});
