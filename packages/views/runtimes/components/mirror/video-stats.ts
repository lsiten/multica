import { z } from "zod";

const counter = z.number().finite().nonnegative().optional();
const inboundVideo = z.object({
  type: z.literal("inbound-rtp"),
  kind: z.literal("video"),
  id: z.string(),
  timestamp: z.number().finite(),
  transportId: z.string().optional(),
  bytesReceived: counter,
  framesDecoded: counter,
  packetsReceived: counter,
  packetsLost: z.number().finite().optional(),
});
const transport = z.object({
  type: z.literal("transport"),
  selectedCandidatePairId: z.string(),
});
const candidatePair = z.object({
  type: z.literal("candidate-pair"),
  state: z.literal("succeeded"),
  currentRoundTripTime: counter,
});
type VideoSample = z.infer<typeof inboundVideo>;

export interface MirrorVideoStats {
  readonly framesPerSecond: number | null;
  readonly bitsPerSecond: number | null;
  readonly packetLossPercent: number | null;
  readonly roundTripTimeMs: number | null;
}

function difference(current: number | undefined, previous: number | undefined): number | null {
  if (current === undefined || previous === undefined || current < previous) return null;
  return current - previous;
}

function measure(report: RTCStatsReport, previous: VideoSample | null) {
  let sample: VideoSample | null = null;
  for (const value of report.values()) {
    const parsed = inboundVideo.safeParse(value);
    if (parsed.success) {
      sample = parsed.data;
      break;
    }
  }
  if (!sample) return { sample, stats: null };
  const selectedTransport = transport.safeParse(report.get(sample.transportId ?? ""));
  const selectedPair = selectedTransport.success
    ? candidatePair.safeParse(report.get(selectedTransport.data.selectedCandidatePairId))
    : null;
  const roundTripTime = selectedPair?.success ? selectedPair.data.currentRoundTripTime : undefined;
  const stats: MirrorVideoStats = {
    framesPerSecond: null,
    bitsPerSecond: null,
    packetLossPercent: null,
    roundTripTimeMs: roundTripTime === undefined ? null : roundTripTime * 1000,
  };
  if (!previous || previous.id !== sample.id || sample.timestamp <= previous.timestamp) {
    return { sample, stats };
  }
  const seconds = (sample.timestamp - previous.timestamp) / 1000;
  const bytes = difference(sample.bytesReceived, previous.bytesReceived);
  const frames = difference(sample.framesDecoded, previous.framesDecoded);
  const received = difference(sample.packetsReceived, previous.packetsReceived);
  const lost = difference(sample.packetsLost, previous.packetsLost);
  return {
    sample,
    stats: {
      ...stats,
      bitsPerSecond: bytes === null ? null : (bytes * 8) / seconds,
      framesPerSecond: frames === null ? null : frames / seconds,
      packetLossPercent: received === null || lost === null || received + lost === 0
        ? null : (lost * 100) / (received + lost),
    },
  };
}

// Keep one previous sample and one pending request per viewer. Browser stats stay
// local and are discarded when that exact session closes or changes source.
export function observeMirrorVideoStats(
  peer: Pick<RTCPeerConnection, "getStats">,
  signal: AbortSignal,
  publish: (stats: MirrorVideoStats | null) => void,
): void {
  let previous: VideoSample | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = () => {
    clearTimeout(timer);
    previous = null;
    publish(null);
  };
  if (signal.aborted) return;
  signal.addEventListener("abort", stop, { once: true });
  const poll = async () => {
    try {
      const report = await peer.getStats();
      if (signal.aborted) return;
      const measured = measure(report, previous);
      previous = measured.sample;
      publish(measured.stats);
    } catch (error) {
      if (signal.aborted) return;
      previous = null;
      publish(null);
      console.warn("Mirror video statistics unavailable", {
        name: error instanceof Error ? error.name : "unknown",
      });
    }
    if (!signal.aborted) timer = setTimeout(() => { void poll(); }, 1000);
  };
  void poll();
}
