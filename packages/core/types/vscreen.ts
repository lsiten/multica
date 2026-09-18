export interface VscreenScope {
  readonly backendIdentity: string;
  readonly accountId: string;
  readonly workspaceId: string;
  readonly runtimeId: string;
}

export interface VscreenResource {
  readonly backendIdentity: string;
  readonly workspaceId: string;
  readonly runtimeId: string;
  readonly uid: number;
}

export interface VscreenEnvelope {
  readonly workspaceId: string;
  readonly runtimeId: string;
  readonly daemonGeneration: string;
  readonly requestId: string;
}

export interface VscreenEpoch {
  readonly nativeEpoch: string;
  readonly displayGeneration: string;
  readonly geometryRevision: number;
}

export type VscreenState =
  | "disabled"
  | "creating"
  | "ready"
  | "suspended"
  | "stopping"
  | "unavailable"
  | "unknown";
export type VscreenControlState =
  | "idle"
  | "agent"
  | "awaiting_takeover"
  | "human"
  | "stopping"
  | "unknown";
export type VscreenPermission =
  | "unknown"
  | "granted"
  | "denied"
  | "restricted"
  | "not_determined";
export type VscreenCommandKind = "enable" | "disable" | "request_takeover";

export interface VscreenStateSnapshot extends VscreenEpoch {
  readonly runtimeId: string;
  readonly state: VscreenState;
  readonly controlState: VscreenControlState;
  readonly activeTaskId: string | null;
  readonly interventionId: string | null;
  readonly permissions: {
    readonly screenRecording: VscreenPermission;
    readonly accessibility: VscreenPermission;
  };
  readonly stateRevision: number;
}

export interface VscreenStateResponse extends VscreenEnvelope {
  readonly state: VscreenStateSnapshot;
}

export interface MirrorSource {
  readonly kind: "virtual" | "physical" | "system";
  readonly sourceId: string;
}

export interface MirrorSourceBinding {
  readonly resource: VscreenResource;
  readonly source: MirrorSource;
  readonly nativeEpoch: string;
  readonly generation: string;
  readonly primary: boolean;
}

export interface VscreenSourceDescriptor extends MirrorSourceBinding {
  readonly name: string;
  readonly width: number;
  readonly height: number;
  readonly scale: number;
}

export interface VscreenSourcesResponse extends VscreenEnvelope {
  readonly sources: readonly VscreenSourceDescriptor[];
}

export interface VscreenCommand {
  readonly commandId: string;
  readonly kind: VscreenCommandKind;
}

export interface VscreenCommandReceipt extends VscreenEnvelope {
  readonly commandId: string;
  readonly receiptId: string;
  readonly state: "pending" | "running" | "succeeded" | "failed" | "unknown";
  readonly reason?: string;
  readonly epoch?: VscreenEpoch;
}

/** Viewing metadata cannot authorize input or resume an AI task. */
export interface MirrorViewerGrant {
  readonly grantId: string;
  readonly sessionId: string;
  readonly workspaceId: string;
  readonly runtimeId: string;
  readonly userId: string;
  readonly viewerId: string;
  readonly nativeEpoch: string;
  readonly source: MirrorSource;
  readonly sourceGeneration: string;
  readonly expiresAt: string;
}

export interface VscreenMirrorRequest {
  readonly viewerId: string;
  readonly offer: { readonly type: "offer"; readonly sdp: string };
  readonly binding: MirrorSourceBinding;
}

export interface VscreenViewerSession {
  readonly sessionId: string;
  readonly viewerId: string;
  readonly binding: MirrorSourceBinding;
}

export type VscreenVideoQuality = VscreenVideoMetadata["quality"];

export interface VscreenMirrorSession {
  readonly id: string;
  readonly workspaceId: string;
  readonly runtimeId: string;
  readonly userId: string;
  readonly daemonId: string;
  readonly viewerId: string;
  readonly createdAt: string;
  readonly expiresAt: string;
  readonly state:
    | "offered"
    | "answered"
    | "failed"
    | "closed"
    | "expired"
    | "unknown";
  readonly failureReason?: string;
  readonly answer?: { readonly type: "answer"; readonly sdp: string };
  readonly iceConfig: {
    readonly iceServers: readonly {
      readonly urls: string | readonly string[];
      readonly username?: string;
      readonly credential?: string;
    }[];
    readonly turnConfigured: boolean;
  };
  readonly viewerGrant?: MirrorViewerGrant;
  readonly videoQuality?: VscreenVideoQuality;
}

/** The immutable session epoch and PTS describe video, never input proof. */
export interface VscreenVideoMetadata {
  readonly type: "mirror:video-meta";
  readonly sourceBinding: MirrorSourceBinding;
  readonly geometryRevision: number;
  /** Nanoseconds can exceed JS integer precision; this value is diagnostic only. */
  readonly ptsNanos: number;
  readonly quality: {
    readonly width: number;
    readonly height: number;
    readonly fps: number;
    readonly bitrate: number;
    readonly maxLevelIdc: number;
  };
}
