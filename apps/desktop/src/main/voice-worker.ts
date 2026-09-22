import { env, pipeline } from "@huggingface/transformers";
import { validVoiceSamples } from "../shared/local-voice";

env.allowRemoteModels = false;
env.useBrowserCache = false;
const parent = process.parentPort;
if (!parent) throw new Error("Voice worker requires an Electron utility process");
const root = process.argv[2];
if (!root) throw new Error("Voice model directory missing");

async function start() {
  const transcriber = await pipeline("automatic-speech-recognition", root, { dtype: "q8", device: "cpu", local_files_only: true });
  parent?.on("message", async ({ data }: { data: unknown }) => {
    if (!data || typeof data !== "object" || !("id" in data) || typeof data.id !== "string" || !("samples" in data) || !validVoiceSamples(data.samples)) return;
    const id = data.id;
    try {
      const output = await transcriber(data.samples, { task: "transcribe", chunk_length_s: 30, stride_length_s: 5, return_timestamps: false });
      const text = (Array.isArray(output) ? output[0]?.text : output.text)?.trim();
      if (!text || text.length > 16 * 1024) throw new Error("Empty or oversized voice transcript");
      parent?.postMessage({ type: "result", id, text });
    } catch { parent?.postMessage({ type: "error", id }); }
  });
  parent?.postMessage({ type: "ready" });
}
void start().catch(() => { parent?.postMessage({ type: "failed" }); });
