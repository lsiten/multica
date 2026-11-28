export interface DictationVolumeMeter {
  connect: (stream: MediaStream) => void;
  close: () => void;
}

/** Prepare during the activation gesture; microphone permission may settle later. */
export function prepareDictationVolume(onLevel: (level: number | null) => void): DictationVolumeMeter {
  let context: AudioContext | undefined;
  let source: MediaStreamAudioSourceNode | undefined;
  let analyser: AnalyserNode | undefined;
  let output: GainNode | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    clearInterval(timer);
    source?.disconnect();
    analyser?.disconnect();
    output?.disconnect();
    if (context && context.state !== "closed") void context.close().catch(() => undefined);
    onLevel(null);
  };
  const connect = (stream: MediaStream) => {
    if (closed || !context) return;
    try {
      source = context.createMediaStreamSource(stream);
      analyser = context.createAnalyser();
      analyser.fftSize = 1024;
      source.connect(analyser);
      output = context.createGain();
      output.gain.value = 0;
      analyser.connect(output);
      output.connect(context.destination);
      const meter = analyser;
      const samples = new Float32Array(meter.fftSize);
      timer = setInterval(() => {
        if (context?.state !== "running") { onLevel(null); return; }
        meter.getFloatTimeDomainData(samples);
        let energy = 0;
        let sum = 0;
        for (const sample of samples) { energy += sample * sample; sum += sample; }
        const mean = sum / samples.length;
        const rms = Math.sqrt(Math.max(0, energy / samples.length - mean * mean));
        // Map -60..-12 dBFS to the display range; this is input level, not speech confidence.
        onLevel(rms > 0 ? Math.max(0, Math.min(1, (20 * Math.log10(rms) + 60) / 48)) : 0);
      }, 80);
    } catch { close(); }
  };
  if (typeof AudioContext !== "undefined") {
    try {
      context = new AudioContext();
      void context.resume().catch(close);
    } catch { close(); }
  }
  return { connect, close };
}
