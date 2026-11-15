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
  readonly displayId?: number;
}

export interface VscreenEnvelope {
  readonly workspaceId: string;
  readonly runtimeId: string;
  readonly daemonGeneration: string;
  readonly requestId: string;
}

export interface MirrorSource {
  readonly kind: "physical" | "system";
  readonly sourceId: string;
}

export type MirrorVideoProfile = "auto" | "clear" | "smooth" | "saver";

export interface MirrorSourceBinding {
  readonly resource: VscreenResource;
  readonly source: MirrorSource;
  readonly nativeEpoch: string;
  readonly generation: string;
  readonly primary: boolean;
  readonly displayId?: number;
}

export interface MirrorSourceDescriptor extends MirrorSourceBinding {
  readonly name: string;
  readonly width: number;
  readonly height: number;
  readonly logicalWidth: number;
  readonly logicalHeight: number;
  readonly scale: number;
  readonly x: number;
  readonly y: number;
  readonly geometryRevision: number;
}

export interface VscreenSourcesResponse extends VscreenEnvelope {
  readonly sources: readonly MirrorSourceDescriptor[];
}

export type AgentDesktopPermissionState =
  | "granted"
  | "denied"
  | "unknown"
  | "not_required"
  | "not_determined";

export type AgentDesktopControlState =
  | "view_only"
  | "waiting_for_approval"
  | "approval_pending"
  | "controlling"
  | "other_controller"
  | "ended";

export interface AgentDesktopControlStatus {
  readonly state: AgentDesktopControlState;
  readonly controlId?: string;
  readonly requestId?: string;
  readonly controllerId?: string;
  readonly requesterId?: string;
  readonly leaseExpiresAt?: string;
  readonly leaseMaxExpiresAt?: string;
}

export type AgentDesktopControlAction =
  | "move"
  | "move_relative"
  | "click"
  | "double_click"
  | "right_click"
  | "mouse_down"
  | "mouse_up"
  | "drag"
  | "scroll"
  | "press"
  | "text"
  | "clipboard_get"
  | "clipboard_set";

export interface AgentDesktopControlInput {
  readonly source: MirrorSource;
  readonly action: AgentDesktopControlAction;
  readonly x?: number;
  readonly y?: number;
  readonly x2?: number;
  readonly y2?: number;
  readonly dx?: number;
  readonly dy?: number;
  readonly button?: "left" | "right" | "middle";
  readonly key?: string;
  readonly text?: string;
}

export interface AgentDesktopReadiness {
  readonly platform: string;
  readonly version: string;
  readonly permissions: {
    readonly accessibility: AgentDesktopPermissionState;
    readonly screenRecording: AgentDesktopPermissionState;
    readonly automation: AgentDesktopPermissionState;
  };
  readonly surfaceCount: number;
  readonly ready: boolean;
  readonly reason?: string;
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
  readonly videoProfile?: MirrorVideoProfile;
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
