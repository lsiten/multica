import { DictationAudioError } from "./chat-dictation-error";

export interface DictationRecording {
  stop: () => void;
  promise: Promise<string>;
  pause?: () => void;
  resume?: () => void;
}

export function recordDictation(
  stream: MediaStream,
  signal: AbortSignal,
  transcribe: (blob: Blob, signal: AbortSignal) => Promise<string>,
): DictationRecording {
  const mimeType = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"]
    .find((type) => MediaRecorder.isTypeSupported(type));
  const recorder = new MediaRecorder(stream, {
    audioBitsPerSecond: 48_000,
    ...(mimeType ? { mimeType } : {}),
  });
  const chunks: Blob[] = [];
  const stop = () => { if (recorder.state !== "inactive") recorder.stop(); };
  const promise = new Promise<string>((resolve, reject) => {
    const abort = () => {
      stop();
      reject(new DOMException("Dictation cancelled", "AbortError"));
    };
    const cleanup = () => signal.removeEventListener("abort", abort);
    recorder.ondataavailable = (event) => {
      if (!signal.aborted && event.data.size) chunks.push(event.data);
    };
    recorder.onerror = () => {
      cleanup();
      reject(new DictationAudioError("recording"));
    };
    recorder.onstop = () => {
      cleanup();
      const blob = new Blob(chunks, { type: recorder.mimeType || "audio/webm" });
      // `stop()` flushes the final data asynchronously; release the source after that flush.
      stream.getTracks().forEach((track) => track.stop());
      if (signal.aborted) return;
      if (!blob.size || blob.size > 512 * 1024) {
        reject(new DictationAudioError("empty"));
        return;
      }
      void transcribe(blob, signal).then(resolve, reject);
    };
    signal.addEventListener("abort", abort, { once: true });
    try {
      recorder.start();
    } catch (error) {
      cleanup();
      reject(error);
    }
  });
  return {
    stop, promise,
    pause: () => { if (recorder.state === "recording") recorder.pause(); },
    resume: () => { if (recorder.state === "paused") recorder.resume(); },
  };
}
