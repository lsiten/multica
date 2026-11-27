// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { Tensor, type LogitsProcessorList } from "@huggingface/transformers";

const transcriber = vi.hoisted(() => vi.fn(async (_samples: Float32Array, options: { language: string; logits_processor: LogitsProcessorList }) => {
  options.logits_processor._call([], new Tensor("float32", new Float32Array([10, 0]), [1, 2]));
  return { text: "中文" };
}));
vi.mock("@huggingface/transformers", async (importOriginal) => ({ ...await importOriginal<typeof import("@huggingface/transformers")>(), env: {}, pipeline: async () => transcriber }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); vi.resetModules(); });

it.each([
  { name: "audible audio", samples: Float32Array.from({ length: 16000 }, (_, i) => 0.1 * Math.sin(i / 10)), accepted: true },
  { name: "silence", samples: new Float32Array(16000), accepted: false },
  { name: "captured Opus silence residue", samples: new Float32Array(16000).fill(2.0345869483764863e-34), accepted: false },
  { name: "DC offset", samples: new Float32Array(16000).fill(0.1), accepted: false },
  { name: "isolated click", samples: Float32Array.from({ length: 16000 }, (_, i) => i === 100 ? 0.8 : 0), accepted: false },
])("transcribes $name only when audio contains a signal", async ({ samples, accepted }) => {
  const listeners = new Map<string, (event: { data: unknown }) => Promise<void>>();
  const postMessage = vi.fn();
  vi.stubGlobal("process", {
    ...process,
    argv: ["electron", "voice-worker", "/unused-voice-test"],
    parentPort: { on: (event: string, listener: (event: { data: unknown }) => Promise<void>) => listeners.set(event, listener), postMessage },
  });
  await import("./voice-worker");
  const receive = listeners.get("message");
  if (!receive) throw new Error("Worker message handler missing");

  await receive({ data: { id: "recording", samples, language: "zh" } });

  if (accepted) {
    expect(transcriber.mock.calls[0]?.[1].language).toBe("zh");
    expect(postMessage).toHaveBeenCalledWith({ type: "result", id: "recording", text: "中文" });
  } else {
    expect(transcriber).not.toHaveBeenCalled();
    expect(postMessage).toHaveBeenCalledWith({ type: "error", id: "recording", reason: "no_signal" });
    expect(postMessage.mock.calls.some(([message]) => message.type === "result")).toBe(false);
  }
});
