// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { prepareVscreenReceiveOffer } from "./vscreen-receive-offer.mjs";
const sdp = ["v=0", "a=ice-ufrag:untouched", "a=ice-pwd:untouched-password", "m=audio 9 UDP/TLS/RTP/SAVPF 111", "a=sendrecv", "a=rtpmap:111 opus/48000/2", "m=video 9 UDP/TLS/RTP/SAVPF 96 97", "a=recvonly", "a=rtpmap:96 H264/90000", "a=fmtp:96 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", "a=rtpmap:97 rtx/90000", "a=fmtp:97 apt=96", "a=candidate:unchanged", ""].join("\r\n");
const offer = { type: "offer" as const, sdp };
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
it("queries the bounded WebRTC target and changes only one receive fmtp parameter", async () => {
  const decodingInfo = vi.fn().mockResolvedValue({ supported: true, smooth: true, powerEfficient: false });
  const result = await prepareVscreenReceiveOffer(offer, { decodingInfo });
  expect(decodingInfo).toHaveBeenCalledExactlyOnceWith({ type: "webrtc", video: { contentType: "video/H264;level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e028", width: 1600, height: 900, bitrate: 20000000, framerate: 30 } });
  expect(result.sdp).toBe(sdp.replace("profile-level-id=42e01f", "profile-level-id=42e01f;max-recv-level=e028"));
  expect(offer.sdp).toBe(sdp);
});
it.each(["42c01f", "42e020"])("preserves CB profile and original level %s", async (profile) => {
  const input = { ...offer, sdp: sdp.replace("42e01f", profile) };
  const output = await prepareVscreenReceiveOffer(input, { decodingInfo: async () => ({ supported: true, smooth: true }) });
  expect(output.sdp).toContain(`profile-level-id=${profile};max-recv-level=${profile.slice(2, 4)}28`);
});
it.each(["sendrecv", "sendonly", "inactive", "missing-direction", "duplicate-direction", "high-profile", "unknown-level", "already-level4", "packetization0", "no-asymmetry", "duplicate-profile", "max-recv", "max-fs", "rejected-track"])("does not query or rewrite ineligible %s", async (mode) => {
  let input = sdp;
  if (["sendrecv", "sendonly", "inactive"].includes(mode)) input = input.replace("a=recvonly", `a=${mode}`);
  if (mode === "missing-direction") input = input.replace("a=recvonly\r\n", "");
  if (mode === "duplicate-direction") input = input.replace("a=recvonly", "a=recvonly\r\na=recvonly");
  if (mode === "high-profile") input = input.replace("42e01f", "64001f");
  if (mode === "unknown-level") input = input.replace("42e01f", "42e027");
  if (mode === "already-level4") input = input.replace("42e01f", "42e028");
  if (mode === "packetization0") input = input.replace("packetization-mode=1", "packetization-mode=0");
  if (mode === "no-asymmetry") input = input.replace("level-asymmetry-allowed=1", "level-asymmetry-allowed=0");
  if (mode === "duplicate-profile") input = input.replace("42e01f", "42e01f;profile-level-id=42e01f");
  if (mode === "max-recv") input = input.replace("42e01f", "42e01f;max-recv-level=e020");
  if (mode === "max-fs") input = input.replace("42e01f", "42e01f;max-fs=3600");
  if (mode === "rejected-track") input = input.replace("m=video 9", "m=video 0");
  const candidate = { ...offer, sdp: input }, decodingInfo = vi.fn();
  expect(await prepareVscreenReceiveOffer(candidate, { decodingInfo })).toBe(candidate);
  expect(decodingInfo).not.toHaveBeenCalled();
});
it.each(["absent", "unsupported", "not-smooth", "error", "timeout"])("retains the original offer on %s", async (mode) => {
  vi.stubGlobal("navigator", {});
  vi.useFakeTimers();
  const query = vi.fn(async () => { if (mode === "error") throw new Error("unavailable"); if (mode === "timeout") return new Promise<{ supported: boolean; smooth: boolean }>(() => {}); return { supported: mode !== "unsupported", smooth: mode !== "not-smooth" }; });
  const pending = prepareVscreenReceiveOffer(offer, mode === "absent" ? {} : { decodingInfo: query, timeoutMs: 20 });
  await vi.advanceTimersByTimeAsync(20);
  expect(await pending).toBe(offer);
  expect(vi.getTimerCount()).toBe(0);
});
it("fences cancellation and ignores a later successful query", async () => {
  const controller = new AbortController();
  let resolve!: (result: { supported: boolean; smooth: boolean }) => void;
  const pending = prepareVscreenReceiveOffer(offer, { signal: controller.signal, decodingInfo: () => new Promise((done) => { resolve = done; }) });
  const rejected = expect(pending).rejects.toMatchObject({ name: "AbortError" });
  controller.abort(); await rejected;
  resolve({ supported: true, smooth: true });
  await expect(prepareVscreenReceiveOffer(offer, { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
});
it("does not declare more than the sender target or upgrade sufficient 720p", async () => {
  const decodingInfo = vi.fn();
  for (const target of [{ width: 1920, height: 1080, bitrate: 20000000, framerate: 30 }, { width: 1280, height: 720, bitrate: 20000000, framerate: 30 }]) expect(await prepareVscreenReceiveOffer(offer, { decodingInfo, target })).toBe(offer);
  expect(decodingInfo).not.toHaveBeenCalled();
});
it("can be serialized and run with no imported closure", async () => {
  const serialized = Function(`return (${prepareVscreenReceiveOffer.toString()})`)() as typeof prepareVscreenReceiveOffer;
  const result = await serialized(offer, { decodingInfo: async () => ({ supported: true, smooth: true }) });
  expect(result.sdp).toContain("max-recv-level=e028");
});
