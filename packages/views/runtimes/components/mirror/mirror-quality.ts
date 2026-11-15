import type { MirrorVideoProfile } from "@multica/core/types";

export type MirrorQualityMode = MirrorVideoProfile;

export function parseMirrorQualityMode(value: string | null): MirrorQualityMode {
  return value === "clear" || value === "smooth" || value === "saver" ? value : "auto";
}

export function selectAdaptiveVideoProfile(input: {
  readonly current: MirrorVideoProfile;
  readonly roundTripTimeMs: number | null | undefined;
  readonly packetLossPercent: number | null | undefined;
  readonly framesPerSecond: number | null | undefined;
  readonly targetFramesPerSecond: number | null | undefined;
}): MirrorVideoProfile {
  const rtt = input.roundTripTimeMs ?? null;
  const loss = input.packetLossPercent ?? null;
  const fps = input.framesPerSecond ?? null;
  const target = input.targetFramesPerSecond ?? null;
  if (loss !== null && loss >= 8) return "saver";
  if (fps !== null && fps < 10) return "saver";
  if (
    (rtt !== null && rtt > 180) ||
    (loss !== null && loss >= 3) ||
    (fps !== null && fps < 18)
  ) {
    return "smooth";
  }
  if (
    rtt !== null &&
    rtt <= 70 &&
    (loss === null || loss < 0.5) &&
    fps !== null &&
    target !== null &&
    fps >= target * 0.95
  ) {
    return "clear";
  }
  return input.current === "saver" && rtt !== null && rtt <= 100 && (loss ?? 0) < 1
    ? "smooth"
    : input.current;
}
