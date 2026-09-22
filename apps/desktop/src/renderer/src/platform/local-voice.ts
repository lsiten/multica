import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
import type { LocalVoiceAPI } from "../../../shared/local-voice";

export function createLocalVoiceAdapter(api: LocalVoiceAPI): LocalVoiceAdapter {
  let status: LocalVoiceStatus = { phase: "downloading", percent: 0 };
  let revision = 0;
  const listeners = new Set<() => void>();
  const publish = (next: LocalVoiceStatus) => { status = next; listeners.forEach((listener) => listener()); };
  api.onStatus((next) => { revision++; publish(next); });
  const initialRevision = revision;
  void api.getStatus().then((next) => { if (revision === initialRevision) publish(next); }).catch(() => publish({ phase: "failed", percent: 0 }));
  return {
    getSnapshot: () => status,
    subscribe: (listener) => { listeners.add(listener); return () => listeners.delete(listener); },
    retry: () => { void api.retry().catch(() => publish({ phase: "failed", percent: 0 })); },
    transcribe: async (recording, signal) => {
      signal.throwIfAborted();
      const id = crypto.randomUUID();
      const audio = new AudioContext({ sampleRate: 16_000 });
      let samples: Float32Array;
      try {
        const decoded = await audio.decodeAudioData(await recording.arrayBuffer());
        if (decoded.duration > 61) throw new Error("Voice recording too long");
        samples = new Float32Array(decoded.length);
        for (let channel = 0; channel < decoded.numberOfChannels; channel++) {
          const data = decoded.getChannelData(channel);
          for (let i = 0; i < samples.length; i++) samples[i] += (data[i] ?? 0) / decoded.numberOfChannels;
        }
        samples = samples.map((sample) => Math.max(-1, Math.min(1, sample)));
      } finally { await audio.close(); }
      signal.throwIfAborted();
      return new Promise<string>((resolve, reject) => {
        const cancel = () => { api.cancel(id); reject(new DOMException("Cancelled", "AbortError")); };
        signal.addEventListener("abort", cancel, { once: true });
        void api.transcribe(id, samples).then((text) => { if (!signal.aborted) resolve(text); }, reject)
          .finally(() => signal.removeEventListener("abort", cancel));
      });
    },
    transcribeStream: (input, signal, onPartial) => {
      signal.throwIfAborted();
      const stream = input as MediaStream;
      let stopped = false;
      let activeId: string | undefined;
      let samples = new Float32Array(0);
      let processed = 0;
      let busy: Promise<void> | undefined;
      let latest = "";
      let resolveFinal!: (text: string) => void;
      let rejectFinal!: (error: unknown) => void;
      const promise = new Promise<string>((resolve, reject) => { resolveFinal = resolve; rejectFinal = reject; });
      const audio = new AudioContext({ sampleRate: 16_000 });
      const source = audio.createMediaStreamSource(stream);
      const workletURL = URL.createObjectURL(new Blob([`class CaptureProcessor extends AudioWorkletProcessor { process(inputs) { const input = inputs[0]?.[0]; if (input) this.port.postMessage(input.slice()); return true; } } registerProcessor("multica-voice-capture", CaptureProcessor);`], { type: "application/javascript" }));
      let node: AudioWorkletNode | undefined;
      const append = (chunk: Float32Array) => {
        const next = new Float32Array(samples.length + chunk.length);
        next.set(samples); next.set(chunk, samples.length); samples = next;
      };
      const cleanup = () => {
        node?.disconnect();
        source.disconnect();
        stream.getTracks().forEach((track) => track.stop());
        URL.revokeObjectURL(workletURL);
        void audio.close().catch(() => undefined);
      };
      const abort = () => {
        stopped = true;
        if (activeId) api.cancel(activeId);
        cleanup();
        rejectFinal(new DOMException("Cancelled", "AbortError"));
      };
      signal.addEventListener("abort", abort, { once: true });
      void promise.catch(() => undefined);
      const run = async (final: boolean) => {
        if (signal.aborted) return;
        if (busy || samples.length === processed && !final) return;

        if (!final && samples.length - processed < 2 * 16_000) return;
        const start = final ? 0 : Math.max(0, samples.length - 4 * 16_000);
        const chunk = samples.slice(start);
        processed = samples.length;
        if (!final) {
          let energy = 0;
          for (const sample of chunk) energy += sample * sample;
          if (Math.sqrt(energy / Math.max(1, chunk.length)) < 0.008) return;
        }
        const id = crypto.randomUUID();
        activeId = id;
        busy = api.transcribe(id, chunk).then((text) => {
          if (signal.aborted) return;
          latest = text;
          onPartial(text);
        }).catch((error: unknown) => { if (final) throw error; })
          .finally(() => { busy = undefined; activeId = undefined; });
        await busy;
      };
      const finish = async () => {
        if (stopped) return;
        stopped = true;
        node?.disconnect(); source.disconnect(); stream.getTracks().forEach((track) => track.stop());
        try {
          await busy;
          signal.throwIfAborted();
          await run(true);
          signal.throwIfAborted();
          if (latest) resolveFinal(latest); else rejectFinal(new Error("Voice transcription failed"));
        } catch (error) { rejectFinal(error); }
        finally { signal.removeEventListener("abort", abort); cleanup(); }
      };
      void audio.audioWorklet.addModule(workletURL).then(() => {
        if (stopped || signal.aborted) return;
        node = new AudioWorkletNode(audio, "multica-voice-capture");
        node.port.onmessage = (event: MessageEvent<Float32Array>) => { if (!stopped) { append(event.data); void run(false); } };
        source.connect(node); node.connect(audio.destination); void audio.resume();
      }).catch((error: unknown) => { stopped = true; signal.removeEventListener("abort", abort); cleanup(); rejectFinal(error); });
      return { stop: () => { void finish(); }, promise };
    },
  };
}
