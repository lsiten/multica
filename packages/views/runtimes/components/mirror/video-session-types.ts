import type {
  MirrorControlGrant,
  MirrorInputAck,
  MirrorInputMessage,
  MirrorPointerButton,
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
  readonly control?: (state: MirrorControlState) => void;
}
export class MirrorVideoError extends Error {
  constructor(readonly reason: string) {
    super(reason);
    this.name = "MirrorVideoError";
  }
}


export type MirrorControlStatus =
  | "inactive"
  | "requesting"
  | "active"
  | "failed";

export interface MirrorControlState {
  readonly status: MirrorControlStatus;
  readonly grant?: MirrorControlGrant;
  readonly reason?: string;
  readonly lastAck?: MirrorInputAck;
}

export interface MirrorControlPointer {
  readonly button?: MirrorPointerButton;
  readonly x: number;
  readonly y: number;
  readonly deltaX?: number;
  readonly deltaY?: number;
}

export interface MirrorControlKey {
  readonly key: string;
  readonly modifiers?: readonly ("shift" | "control" | "alt" | "meta")[];
}

export interface MirrorControlText {
  readonly text: string;
}

export interface MirrorControlInput {
  readonly kind: MirrorInputMessage["kind"];
  readonly gestureId: string;
  readonly pointer?: MirrorControlPointer;
  readonly key?: MirrorControlKey;
  readonly text?: MirrorControlText;
}
