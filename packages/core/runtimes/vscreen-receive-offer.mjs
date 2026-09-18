/**
 * Declare queried H264 receive capacity without replacing the browser's codec.
 * Self-contained so the same function can run in a private browser page.
 * This is a support estimate for the sender's bounded target, not sustained FPS
 * or a certification of every stream permitted by H264 Level 4 (RFC 6184 §8.1).
 */
export async function prepareVscreenReceiveOffer(offer, options = {}) {
  const signal = options.signal;
  const aborted = () => {
    if (signal?.aborted) throw new globalThis.DOMException("Receive offer cancelled", "AbortError");
  };
  aborted();
  if (offer?.type !== "offer" || typeof offer.sdp !== "string" || offer.sdp.length > 65536) return offer;
  const target = options.target ?? { width: 1600, height: 900, bitrate: 20000000, framerate: 30 };
  if (![target.width, target.height, target.bitrate, target.framerate].every((n) => Number.isFinite(n) && n > 0) || !Number.isInteger(target.width) || !Number.isInteger(target.height) || target.width > 1600 || target.height > 900 || target.bitrate > 20000000 || target.framerate > 30) return offer;
  let query = options.decodingInfo;
  if (!query) {
    const capabilities = globalThis.navigator?.mediaCapabilities;
    if (typeof capabilities?.decodingInfo !== "function") return offer;
    query = (configuration) => capabilities.decodingInfo(configuration);
  }
  const timeout = options.timeoutMs ?? 750;
  if (!Number.isFinite(timeout) || timeout <= 0) return offer;
  const lines = offer.sdp.split("\n");
  const text = (line) => line.replace(/\r$/, "");
  const candidates = [];
  for (let start = 0; start < lines.length; start++) {
    const media = /^m=video ([1-9]\d*) UDP\/TLS\/RTP\/SAVPF (.+)$/.exec(text(lines[start]));
    if (!media) continue;
    let end = start + 1;
    while (end < lines.length && !lines[end].startsWith("m=")) end++;
    const section = lines.slice(start + 1, end).map(text);
    const directions = section.filter((line) => /^a=(recvonly|sendonly|sendrecv|inactive)$/.test(line));
    if (directions.length !== 1 || directions[0] !== "a=recvonly") continue;
    const payloads = media[2].split(" ");
    if (payloads.some((pt) => !/^\d+$/.test(pt) || Number(pt) > 127) || new Set(payloads).size !== payloads.length) continue;
    for (const pt of payloads) {
      const maps = section.filter((line) => line.startsWith(`a=rtpmap:${pt} `));
      if (maps.length !== 1 || !new RegExp(`^a=rtpmap:${pt} H264/90000$`, "i").test(maps[0])) continue;
      const indexes = [];
      for (let i = start + 1; i < end; i++) if (lines[i].startsWith(`a=fmtp:${pt} `)) indexes.push(i);
      if (indexes.length !== 1) continue;
      const index = indexes[0];
      const entries = text(lines[index]).slice(`a=fmtp:${pt} `.length).split(";").map((part) => part.trim().split("="));
      if (entries.some((entry) => entry.length !== 2 || !entry[0] || !entry[1])) continue;
      const parameters = new Map(entries.map(([key, value]) => [key.toLowerCase(), value]));
      if (parameters.size !== entries.length || [...parameters.keys()].some((key) => key.startsWith("max-"))) continue;
      const profile = parameters.get("profile-level-id")?.toLowerCase();
      if (!/^42(?:c0|e0)(?:1f|20)$/.test(profile ?? "") || parameters.get("packetization-mode") !== "1" || parameters.get("level-asymmetry-allowed") !== "1") continue;
      const blocks = Math.ceil(target.width / 16) * Math.ceil(target.height / 16);
      const currentMax = profile.endsWith("1f") ? 3600 : 5120;
      if (blocks <= currentMax && blocks * target.framerate <= (currentMax === 3600 ? 108000 : 216000)) continue;
      candidates.push({ index, profile, receiveLevel: profile.slice(2, 4) + "28" });
    }
    start = end - 1;
  }
  if (!candidates.length || candidates.length > 16) return offer;
  const profiles = [...new Set(candidates.map((candidate) => candidate.profile.slice(0, 4)))];
  let timer;
  let cancel;
  try {
    const supported = await new Promise((resolve, reject) => {
      cancel = () => reject(new globalThis.DOMException("Receive offer cancelled", "AbortError"));
      signal?.addEventListener("abort", cancel, { once: true });
      timer = globalThis.setTimeout(() => resolve(new Set()), Math.min(timeout, 2000));
      Promise.all(profiles.map(async (profile) => {
        try {
          const result = await query({ type: "webrtc", video: { contentType: `video/H264;level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=${profile}28`, ...target } });
          return result?.supported === true && result?.smooth === true ? profile : null;
        } catch { return null; }
      })).then((values) => resolve(new Set(values.filter(Boolean))));
      if (signal?.aborted) cancel();
    });
    aborted();
    let changed = false;
    for (const candidate of candidates) {
      if (!supported.has(candidate.profile.slice(0, 4))) continue;
      lines[candidate.index] = lines[candidate.index].replace(/(\r?)$/, `;max-recv-level=${candidate.receiveLevel}$1`);
      changed = true;
    }
    return changed ? { ...offer, type: offer.type, sdp: lines.join("\n") } : offer;
  } finally {
    globalThis.clearTimeout(timer);
    if (cancel) signal?.removeEventListener("abort", cancel);
  }
}
