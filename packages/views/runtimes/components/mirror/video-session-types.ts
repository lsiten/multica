import type {
  VscreenVideoMetadata,
  VscreenVideoQuality,
} from "@multica/core/types";
export type MirrorVideoState =
  | "preparing"
  | "negotiating"
  | "decoding"
  | "streaming"
  | "failed"
  | "closed";
export interface MirrorVideoCallbacks {
  readonly state: (state: MirrorVideoState, reason?: string) => void;
  readonly stream: (stream: MediaStream | null) => void;
  readonly quality?: (quality: VscreenVideoQuality | null) => void;
  readonly metadata: (metadata: VscreenVideoMetadata | null) => void;
}
export class MirrorVideoError extends Error {
  constructor(readonly reason: string) {
    super(reason);
    this.name = "MirrorVideoError";
  }
}
