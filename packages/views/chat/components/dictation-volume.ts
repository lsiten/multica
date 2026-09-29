/** Observes the existing microphone stream without opening another capture. */
export function observeDictationVolume(stream: MediaStream, onLevel: (level: number) => void): () => void {
  let context: AudioContext | undefined;
  let source: MediaStreamAudioSourceNode | undefined;
  let analyser: AnalyserNode | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    clearInterval(timer);
    source?.disconnect();
    analyser?.disconnect();
    if (context && context.state !== "closed") void context.close().catch(() => undefined);
    onLevel(0);
  };
  if (typeof AudioContext === "undefined") return close;
  try {
    context = new AudioContext();
    source = context.createMediaStreamSource(stream);
    analyser = context.createAnalyser();
    analyser.fftSize = 256;
    source.connect(analyser);
    const meter = analyser;
    const samples = new Uint8Array(meter.fftSize);
    timer = setInterval(() => {
      meter.getByteTimeDomainData(samples);
      let energy = 0;
      for (const sample of samples) energy += ((sample - 128) / 128) ** 2;
      onLevel(Math.min(1, Math.sqrt(energy / samples.length) * 3.5));
    }, 80);
    void context.resume().catch(close);
  } catch {
    // Meter availability must not prevent transcription; release partial setup.
    close();
  }
  return close;
}
