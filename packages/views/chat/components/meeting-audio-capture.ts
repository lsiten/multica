const TARGET_SAMPLE_RATE = 16_000;
const DEFAULT_CHUNK_DURATION_MS = 2_000;

export interface MeetingAudioCapture {
  start: () => void;
  stop: () => Promise<void>;
  cancel: () => void;
  pause: () => void;
  resume: () => void;
}

export function createMeetingAudioCapture({
  stream,
  onChunk,
  onError,
  chunkDurationMs = DEFAULT_CHUNK_DURATION_MS,
}: {
  readonly stream: MediaStream;
  readonly onChunk: (chunk: Blob) => void;
  readonly onError: (error: unknown) => void;
  readonly chunkDurationMs?: number;
}): MeetingAudioCapture {
  const context = new AudioContext();
  const source = context.createMediaStreamSource(stream);
  const processor = context.createScriptProcessor(4_096, 1, 1);
  const silentOutput = context.createGain();
  silentOutput.gain.value = 0;
  source.connect(processor);
  processor.connect(silentOutput);
  silentOutput.connect(context.destination);
  void context.resume();

  const chunkSampleCount = Math.max(1, Math.round((context.sampleRate * chunkDurationMs) / 1_000));
  let samples: number[] = [];
  let paused = false;
  let started = false;
  let stopped = false;
  let cancelled = false;
  let closePromise: Promise<void> | undefined;

  const emitChunk = (chunkSamples: readonly number[]) => {
    if (cancelled || chunkSamples.length === 0) return;
    onChunk(encodePcm16Wav(resample(chunkSamples, context.sampleRate, TARGET_SAMPLE_RATE)));
  };

  processor.onaudioprocess = (event) => {
    if (!started || stopped || cancelled || paused) return;
    try {
      const input = event.inputBuffer.getChannelData(0);
      samples = [...samples, ...input];
      while (samples.length >= chunkSampleCount) {
        emitChunk(samples.slice(0, chunkSampleCount));
        samples = samples.slice(chunkSampleCount);
      }
    } catch (error) {
      stopped = true;
      onError(error);
      void close();
    }
  };

  const close = () => {
    if (!closePromise) {
      source.disconnect();
      processor.disconnect();
      silentOutput.disconnect();
      closePromise = context.close().then(() => undefined);
    }
    return closePromise;
  };

  return {
    start: () => {
      if (stopped || cancelled) return;
      started = true;
      void context.resume();
    },
    stop: async () => {
      if (stopped || cancelled) return;
      stopped = true;
      emitChunk(samples);
      samples = [];
      await close();
    },
    cancel: () => {
      if (stopped || cancelled) return;
      cancelled = true;
      stopped = true;
      samples = [];
      void close();
    },
    pause: () => { paused = true; },
    resume: () => { if (!stopped && !cancelled) paused = false; },
  };
}

function resample(samples: readonly number[], sourceRate: number, targetRate: number): Float32Array {
  if (sourceRate === targetRate) return Float32Array.from(samples);
  const targetLength = Math.max(1, Math.round(samples.length * targetRate / sourceRate));
  const output = new Float32Array(targetLength);
  const ratio = sourceRate / targetRate;
  for (let index = 0; index < targetLength; index += 1) {
    const sourcePosition = index * ratio;
    const left = Math.floor(sourcePosition);
    const right = Math.min(left + 1, samples.length - 1);
    const fraction = sourcePosition - left;
    output[index] = (samples[left] ?? 0) * (1 - fraction) + (samples[right] ?? 0) * fraction;
  }
  return output;
}

function encodePcm16Wav(samples: Float32Array): Blob {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeAscii(view, 0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  writeAscii(view, 8, "WAVE");
  writeAscii(view, 12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, TARGET_SAMPLE_RATE, true);
  view.setUint32(28, TARGET_SAMPLE_RATE * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeAscii(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  for (let index = 0; index < samples.length; index += 1) {
    const sample = Math.max(-1, Math.min(1, samples[index] ?? 0));
    view.setInt16(44 + index * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
  }
  return new Blob([buffer], { type: "audio/wav" });
}

function writeAscii(view: DataView, offset: number, value: string) {
  for (let index = 0; index < value.length; index += 1) view.setUint8(offset + index, value.charCodeAt(index));
}
