// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { observeMirrorVideoStats, type MirrorVideoStats } from "./video-stats";

function report(input: { timestamp: number; bytesReceived?: number; framesDecoded?: number; packetsReceived?: number; packetsLost?: number; id?: string }) {
  return new Map<string, unknown>([
    ["video", { type: "inbound-rtp", kind: "video", id: "video", transportId: "transport", ...input }],
    ["transport", { type: "transport", selectedCandidatePairId: "selected" }],
    ["selected", { type: "candidate-pair", state: "succeeded", currentRoundTripTime: 0.025 }],
    ["unselected", { type: "candidate-pair", state: "succeeded", currentRoundTripTime: 9 }],
  ]);
}

afterEach(() => { vi.useRealTimers(); });

it("reports received bitrate, decoded frames, interval loss and the selected transport RTT", async () => {
  vi.useFakeTimers();
  const controller = new AbortController();
  const publish = vi.fn<(stats: MirrorVideoStats | null) => void>();
  const peer = { getStats: vi.fn<() => Promise<RTCStatsReport>>()
    .mockResolvedValueOnce(report({ timestamp: 1000, bytesReceived: 1000, framesDecoded: 10, packetsReceived: 100, packetsLost: 2 }))
    .mockResolvedValue(report({ timestamp: 3000, bytesReceived: 501000, framesDecoded: 50, packetsReceived: 198, packetsLost: 4 })) };
  observeMirrorVideoStats(peer, controller.signal, publish);
  await vi.advanceTimersByTimeAsync(1000);
  expect(publish).toHaveBeenLastCalledWith({ framesPerSecond: 20, bitsPerSecond: 2_000_000, packetLossPercent: 2, roundTripTimeMs: 25 });
  controller.abort();
});

it("resets rates across stream identity or counter resets and keeps missing metrics unknown", async () => {
  vi.useFakeTimers();
  const controller = new AbortController();
  const publish = vi.fn<(stats: MirrorVideoStats | null) => void>();
  const peer = { getStats: vi.fn<() => Promise<RTCStatsReport>>()
    .mockResolvedValueOnce(report({ timestamp: 1000, bytesReceived: 10000, framesDecoded: 10 }))
    .mockResolvedValueOnce(report({ timestamp: 2000, bytesReceived: 100, framesDecoded: 1 }))
    .mockResolvedValueOnce(report({ timestamp: 3000, bytesReceived: 10000, framesDecoded: 100, id: "new-stream" }))
    .mockResolvedValue(report({ timestamp: 4000, id: "new-stream" })) };
  observeMirrorVideoStats(peer, controller.signal, publish);
  for (let sample = 0; sample < 3; sample++) {
    await vi.advanceTimersByTimeAsync(1000);
    expect(publish).toHaveBeenLastCalledWith({ framesPerSecond: null, bitsPerSecond: null, packetLossPercent: null, roundTripTimeMs: 25 });
  }
  controller.abort();
});

it("never overlaps pending requests or publishes a delayed sample after session close", async () => {
  vi.useFakeTimers();
  const controller = new AbortController();
  const publish = vi.fn<(stats: MirrorVideoStats | null) => void>();
  const pending = Promise.withResolvers<RTCStatsReport>();
  const peer = { getStats: vi.fn(() => pending.promise) };
  observeMirrorVideoStats(peer, controller.signal, publish);
  await vi.advanceTimersByTimeAsync(10_000);
  expect(peer.getStats).toHaveBeenCalledTimes(1);
  controller.abort();
  pending.resolve(report({ timestamp: 1000, bytesReceived: 100, framesDecoded: 1 }));
  await vi.advanceTimersByTimeAsync(10_000);
  expect(publish.mock.calls).toEqual([[null]]);
  expect(peer.getStats).toHaveBeenCalledTimes(1);
});

it("clears unavailable statistics and resumes with a fresh counter baseline", async () => {
  vi.useFakeTimers();
  const warning = vi.spyOn(console, "warn").mockImplementation(() => undefined);
  const controller = new AbortController();
  const publish = vi.fn<(stats: MirrorVideoStats | null) => void>();
  const peer = { getStats: vi.fn<() => Promise<RTCStatsReport>>()
    .mockResolvedValueOnce(report({ timestamp: 1000, bytesReceived: 1000, framesDecoded: 10 }))
    .mockRejectedValueOnce(new DOMException("peer unavailable", "InvalidStateError"))
    .mockResolvedValue(report({ timestamp: 5000, bytesReceived: 9000, framesDecoded: 50 })) };
  observeMirrorVideoStats(peer, controller.signal, publish);
  await vi.advanceTimersByTimeAsync(1000);
  expect(publish).toHaveBeenLastCalledWith(null);
  await vi.advanceTimersByTimeAsync(1000);
  expect(publish).toHaveBeenLastCalledWith({ framesPerSecond: null, bitsPerSecond: null, packetLossPercent: null, roundTripTimeMs: 25 });
  expect(warning).toHaveBeenCalledWith("Mirror video statistics unavailable", { name: "InvalidStateError" });
  controller.abort();
  warning.mockRestore();
});
