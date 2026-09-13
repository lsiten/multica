import type { MirrorICEConfig, MirrorICEServer } from "@multica/core/types";

type IceGatheringPeer = EventTarget &
  Pick<
    RTCPeerConnection,
    "iceGatheringState" | "iceConnectionState" | "addEventListener" | "removeEventListener"
  >;

export function peerIceServers(servers: readonly MirrorICEServer[]): RTCIceServer[] {
  return servers.map((server) => ({
    urls: typeof server.urls === "string" ? server.urls : [...server.urls],
    username: server.username,
    credential: server.credential,
  }));
}

// Time to keep gathering after the first relay candidate. Cloudflare TURN
// returns a relay candidate within ~1s, but waiting for gathering to reach
// "complete" also waits on blocked UDP TURN components to exhaust their
// retransmissions (~8s on restricted networks, >10s on hostile ones), which
// used to trip the hard deadline before any offer was sent. A short grace
// window lets host/srflx candidates arrive without risking that stall.
const RELAY_CANDIDATE_GRACE_MS = 1_000;

type IceCandidatePeer = IceGatheringPeer &
  Pick<RTCPeerConnection, "addEventListener" | "removeEventListener">;

export function waitForUsableIceCandidates(
  peer: IceCandidatePeer,
  hasRelayCandidate: () => boolean,
  timeoutMs = 20_000,
): Promise<void> {
  if (peer.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve, reject) => {
    let settled = false;
    let graceTimer: ReturnType<typeof setTimeout> | null = null;

    const timer = setTimeout(() => {
      cleanup();
      reject(new Error("ICE gathering timed out"));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timer);
      if (graceTimer) clearTimeout(graceTimer);
      peer.removeEventListener("icegatheringstatechange", onStateChange);
      peer.removeEventListener("iceconnectionstatechange", onFailure);
      peer.removeEventListener("icecandidate", onCandidate);
    };
    const finish = () => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve();
    };
    const onStateChange = () => {
      if (peer.iceGatheringState === "complete") finish();
    };
    const onFailure = () => {
      if (peer.iceConnectionState !== "failed") return;
      cleanup();
      reject(new Error("ICE gathering failed"));
    };
    const onCandidate = () => {
      if (!hasRelayCandidate() || graceTimer) return;
      // A relay candidate can traverse symmetric NAT; give the cheap local
      // candidates a brief window to arrive, then start the offer instead of
      // waiting for every blocked TURN transport to give up.
      graceTimer = setTimeout(finish, RELAY_CANDIDATE_GRACE_MS);
    };
    peer.addEventListener("icegatheringstatechange", onStateChange);
    peer.addEventListener("iceconnectionstatechange", onFailure);
    peer.addEventListener("icecandidate", onCandidate);
  });
}

export function peerConfiguration(config: MirrorICEConfig): RTCConfiguration {
  return { iceServers: peerIceServers(config.ice_servers) };
}
