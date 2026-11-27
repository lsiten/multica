// Whisper can produce plausible text for silence, including Opus's tiny DC residue.
// Require at least 160 ms of varying audio above -80 dBFS before inference.
export function hasVoiceSignal(samples: Float32Array): boolean {
  const frameLength = 320;
  let audibleFrames = 0;
  for (let offset = 0; offset + frameLength <= samples.length; offset += frameLength) {
    let sum = 0;
    let energy = 0;
    for (let index = offset; index < offset + frameLength; index += 1) {
      const value = samples[index];
      sum += value;
      energy += value * value;
    }
    const mean = sum / frameLength;
    if (energy / frameLength - mean * mean > 1e-8) audibleFrames += 1;
    if (audibleFrames >= 8) return true;
  }
  return false;
}
