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
  };
}
