export interface VscreenReceiveTarget {
  width: number;
  height: number;
  bitrate: number;
  framerate: number;
}
export interface VscreenReceiveCapabilityQuery {
  type: "webrtc";
  video: VscreenReceiveTarget & { contentType: string };
}
export interface VscreenReceiveOfferOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  /** Actual sender ceiling, never a lower probe-only bitrate. Default: 1600×900 / 20 Mbps / 30 fps. */
  target?: VscreenReceiveTarget;
  decodingInfo?: (configuration: VscreenReceiveCapabilityQuery) => Promise<{ supported: boolean; smooth: boolean }>;
}
/** A bounded receive-capability estimate; does not certify sustained rendering. */
export declare function prepareVscreenReceiveOffer<T extends { type: string; sdp?: string }>(offer: T, options?: VscreenReceiveOfferOptions): Promise<T>;
