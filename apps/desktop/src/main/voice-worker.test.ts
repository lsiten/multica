// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";

const transcriber = vi.hoisted(() => vi.fn(async (_samples: Float32Array, _options: { language: string }) => ({ text: "中文" })));
vi.mock("@huggingface/transformers", () => ({ env: {}, pipeline: async () => transcriber }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); vi.resetModules(); });

it("passes the request language into Whisper inference", async () => {
  const listeners = new Map<string, (event: { data: unknown }) => Promise<void>>();
  const postMessage = vi.fn();
  vi.stubGlobal("process", {
    ...process,
    argv: ["electron", "voice-worker", "/unused-voice-test"],
    parentPort: { on: (event: string, listener: (event: { data: unknown }) => Promise<void>) => listeners.set(event, listener), postMessage },
  });
  await import("./voice-worker");
  const samples = new Float32Array([0.1]);
  const receive = listeners.get("message");
  if (!receive) throw new Error("Worker message handler missing");

  await receive({ data: { id: "recording", samples, language: "zh" } });

  expect(transcriber.mock.calls[0]?.[1].language).toBe("zh");
  expect(postMessage).toHaveBeenCalledWith({ type: "result", id: "recording", text: "中文" });
});
