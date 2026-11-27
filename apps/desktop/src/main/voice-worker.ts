import { env, pipeline, LogitsProcessorList } from "@huggingface/transformers";
import { validVoiceLanguage, validVoiceSamples } from "../shared/local-voice";
import { hasVoiceSignal } from "./voice-signal";
import { VoiceConfidence } from "./voice-confidence";

env.allowRemoteModels = false;
env.useBrowserCache = false;
const parent = process.parentPort;
if (!parent) throw new Error("Voice worker requires an Electron utility process");
const root = process.argv[2];
if (!root) throw new Error("Voice model directory missing");

async function start() {
  const transcriber = await pipeline("automatic-speech-recognition", root, { dtype: "q8", device: "cpu", local_files_only: true });
  parent?.on("message", async ({ data }: { data: unknown }) => {
    if (!data || typeof data !== "object" || !("id" in data) || typeof data.id !== "string" || !("samples" in data) || !validVoiceSamples(data.samples) || !("language" in data) || !validVoiceLanguage(data.language)) return;
    const id = data.id;
    if (!hasVoiceSignal(data.samples)) {
      parent?.postMessage({ type: "error", id, reason: "no_signal" });
      return;
    }
    try {
      const confidence = new VoiceConfidence();
      const processors = new LogitsProcessorList();
      processors.push(confidence);
      const options = { language: data.language, task: "transcribe", chunk_length_s: 30, stride_length_s: 5, return_timestamps: false, logits_processor: processors, do_sample: false };
      const output = await transcriber(data.samples, options);
      const text = (Array.isArray(output) ? output[0]?.text : output.text)?.trim();
      if (!text || !confidence.accepts(text)) throw new Error("Unreliable voice transcript");
      parent?.postMessage({ type: "result", id, text });
    } catch (error) {
      parent?.postMessage({ type: "error", id, reason: error instanceof Error && error.message === "Unreliable voice transcript" ? "unreliable_transcript" : "inference_failed" });
    }
  });
  parent?.postMessage({ type: "ready" });
}
void start().catch(() => { parent?.postMessage({ type: "failed" }); });
