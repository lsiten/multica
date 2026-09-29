export const MIRROR_VOICE_MAX_BYTES = 512 * 1024;

const VOICE_TIMESLICE_MS = 1_500;

export interface MirrorVoiceTransport {
  request(recording: Blob, signal: AbortSignal): Promise<string>;
}

export interface MirrorVoiceStreamHandle {
  stop: () => void;
  promise: Promise<string>;
}

export class MirrorVoiceStreamError extends Error {
  readonly code: "empty_recording" | "recording_too_large" | "recorder_unavailable";

  constructor(
    code: "empty_recording" | "recording_too_large" | "recorder_unavailable",
  ) {
    super(code);
    this.name = "MirrorVoiceStreamError";
    this.code = code;
  }
}

export class MirrorVoiceRequestError extends Error {
  readonly code: string;

  constructor(code: string) {
    super(code);
    this.name = "MirrorVoiceRequestError";
    this.code = code;
  }
}

type Snapshot = {
  readonly recording: Blob;
  readonly final: boolean;
};

function mediaStreamInput(input: unknown): MediaStream {
  if (
    !input ||
    typeof input !== "object" ||
    !("getTracks" in input) ||
    typeof input.getTracks !== "function"
  ) {
    throw new MirrorVoiceStreamError("recorder_unavailable");
  }
  return input as MediaStream;
}

/**
 * Records cumulative, prefix-decodable snapshots while allowing only one
 * transcribe request in flight. A newer snapshot replaces a pending one.
 */
export function startMirrorVoiceStream(
  input: unknown,
  signal: AbortSignal,
  onPartial: (text: string) => void,
  transport: MirrorVoiceTransport,
): MirrorVoiceStreamHandle {
  if (typeof MediaRecorder === "undefined") {
    return {
      stop: () => undefined,
      promise: Promise.reject(new MirrorVoiceStreamError("recorder_unavailable")),
    };
  }

  let stream: MediaStream;
  try {
    stream = mediaStreamInput(input);
  } catch (error) {
    return {
      stop: () => undefined,
      promise: Promise.reject(error),
    };
  }

  let recorder: MediaRecorder;
  try {
    recorder = new MediaRecorder(stream, { audioBitsPerSecond: 48_000 });
  } catch (error) {
    return {
      stop: () => undefined,
      promise: Promise.reject(error),
    };
  }

  const chunks: Blob[] = [];
  let totalBytes = 0;
  let pending: Snapshot | null = null;
  let inFlight = false;
  let stopping = false;
  let cancelled = false;
  let settled = false;
  let resolveFinal!: (text: string) => void;
  let rejectFinal!: (error: unknown) => void;
  const promise = new Promise<string>((resolve, reject) => {
    resolveFinal = resolve;
    rejectFinal = reject;
  });

  const cleanup = () => {
    signal.removeEventListener("abort", abort);
    stream.getTracks().forEach((track) => track.stop());
  };

  const rejectOnce = (error: unknown) => {
    if (settled) return;
    settled = true;
    cleanup();
    rejectFinal(error);
  };

  const resolveOnce = (text: string) => {
    if (settled) return;
    settled = true;
    cleanup();
    resolveFinal(text);
  };

  const failRecording = (error: unknown) => {
    if (settled || cancelled) return;
    cancelled = true;
    pending = null;
    if (recorder.state === "recording") recorder.stop();
    rejectOnce(error);
  };

  const pump = async (): Promise<void> => {
    if (inFlight || !pending || cancelled || signal.aborted) return;
    const snapshot = pending;
    pending = null;
    inFlight = true;
    try {
      const text = await transport.request(snapshot.recording, signal);
      if (cancelled || signal.aborted || settled) return;
      const trimmed = text.trim();
      if (trimmed) onPartial(trimmed);
      if (snapshot.final) {
        if (!trimmed) rejectOnce(new MirrorVoiceStreamError("empty_recording"));
        else resolveOnce(trimmed);
      }
    } catch (error) {
      if (snapshot.final && !cancelled && !signal.aborted) rejectOnce(error);
      // A partial request may fail transiently. Keep recording and let a later
      // cumulative snapshot retry; the final snapshot remains authoritative.
    } finally {
      inFlight = false;
      if (!settled && !cancelled && pending) void pump();
    }
  };

  const enqueue = (final: boolean) => {
    if (cancelled || settled) return;
    if (totalBytes === 0) {
      if (final) rejectOnce(new MirrorVoiceStreamError("empty_recording"));
      return;
    }
    pending = {
      recording: new Blob(chunks, { type: recorder.mimeType || "audio/webm" }),
      final,
    };
    void pump();
  };

  const abort = () => {
    if (cancelled || settled) return;
    cancelled = true;
    pending = null;
    if (recorder.state === "recording") recorder.stop();
    rejectOnce(new DOMException("Mirror voice cancelled", "AbortError"));
  };

  const stop = () => {
    if (stopping || cancelled || settled) return;
    stopping = true;
    if (recorder.state === "recording") recorder.stop();
    else enqueue(true);
  };

  recorder.ondataavailable = (event) => {
    if (cancelled || settled || !event.data.size) return;
    totalBytes += event.data.size;
    if (totalBytes > MIRROR_VOICE_MAX_BYTES) {
      failRecording(new MirrorVoiceStreamError("recording_too_large"));
      return;
    }
    chunks.push(event.data);
    if (!stopping) enqueue(false);
  };
  recorder.onerror = () => failRecording(new Error("Mirror voice recording failed"));
  recorder.onstop = () => {
    if (!cancelled && !settled) enqueue(true);
  };
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) {
    abort();
    return { stop, promise };
  }
  try {
    recorder.start(VOICE_TIMESLICE_MS);
  } catch (error) {
    failRecording(error);
  }
  return { stop, promise };
}
