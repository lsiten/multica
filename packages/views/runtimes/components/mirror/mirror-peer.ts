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

export function waitForIceGatheringComplete(
  peer: IceGatheringPeer,
  timeoutMs = 10_000,
): Promise<void> {
  if (peer.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error("ICE gathering timed out"));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timer);
      peer.removeEventListener("icegatheringstatechange", onStateChange);
      peer.removeEventListener("iceconnectionstatechange", onFailure);
    };
    const onStateChange = () => {
      if (peer.iceGatheringState !== "complete") return;
      cleanup();
      resolve();
    };
    peer.addEventListener("icegatheringstatechange", onStateChange);
    const onFailure = () => {
      if (peer.iceConnectionState !== "failed") return;
      cleanup();
      reject(new Error("ICE gathering failed"));
    };
    peer.addEventListener("iceconnectionstatechange", onFailure, { once: true });
  });
}

export function peerConfiguration(config: MirrorICEConfig): RTCConfiguration {
  return { iceServers: peerIceServers(config.ice_servers) };
}
