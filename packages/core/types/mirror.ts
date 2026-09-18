import type { MirrorViewerGrant } from "./vscreen";

export interface MirrorSessionDescription {
  type: string;
  sdp: string;
}

export interface MirrorICEServer {
  readonly urls: string | readonly string[];
  readonly username?: string;
  readonly credential?: string;
}

export interface MirrorICEConfig {
  readonly ice_servers: readonly MirrorICEServer[];
  readonly turn_configured: boolean;
}

export type MirrorSessionState =
  | "offered"
  | "answered"
  | "failed"
  | "closed"
  | "expired"
  | string;

export interface MirrorSession {
  id: string;
  workspace_id: string;
  runtime_id: string;
  user_id: string;
  daemon_id: string;
  viewer_id: string;
  created_at: string;
  expires_at: string;
  state: MirrorSessionState;
  failure_reason?: string;
}

export interface MirrorSessionResponse extends MirrorSession {
  answer?: MirrorSessionDescription;
  ice_config: MirrorICEConfig;
  viewer_grant?: MirrorViewerGrant;
}

export interface CreateMirrorSessionRequest {
  viewer_id: string;
  offer: MirrorSessionDescription;
}

export type MirrorNetworkMode = "builtin" | "custom" | "disabled";

export type MirrorNetworkSource =
  | "env"
  | "builtin"
  | "custom"
  | "disabled"
  | "builtin_unavailable"
  | string;

export interface MirrorNetworkServerInput {
  readonly urls: string | readonly string[];
  readonly username?: string;
  readonly credential?: string;
}

/** A stored custom ICE server as returned by the API — the secret is never echoed. */
export interface MirrorNetworkServer {
  readonly urls: readonly string[];
  readonly username?: string;
  readonly has_credential: boolean;
}

export interface BuiltinMirrorNetwork {
  readonly enabled: boolean;
  readonly available: boolean;
  readonly host?: string;
  readonly port?: number;
  readonly transports?: readonly string[];
  readonly credential_ttl_seconds?: number;
}

export interface CloudflareMirrorNetwork {
  readonly enabled: boolean;
  readonly available: boolean;
  readonly healthy: boolean;
  readonly key_id?: string;
  readonly has_api_token: boolean;
  readonly credential_ttl_seconds?: number;
}

export interface MirrorNetworkSettings {
  readonly source: MirrorNetworkSource;
  readonly locked: boolean;
  readonly can_manage: boolean;
  readonly turn_configured: boolean;
  readonly mode: MirrorNetworkMode;
  readonly viewer_notifications_enabled: boolean;
  readonly cloudflare: CloudflareMirrorNetwork;
  readonly builtin: BuiltinMirrorNetwork;
  readonly custom: readonly MirrorNetworkServer[];
}

export interface CloudflareMirrorNetworkInput {
  readonly key_id?: string;
  readonly api_token?: string;
  readonly remove?: boolean;
}

export interface UpdateMirrorNetworkRequest {
  readonly mode: MirrorNetworkMode;
  readonly servers?: readonly MirrorNetworkServerInput[];
  readonly cloudflare?: CloudflareMirrorNetworkInput;
  readonly viewer_notifications_enabled?: boolean;
}

export type MirrorEventType =
  | "session_started"
  | "session_answered"
  | "session_failed"
  | "viewer_started"
  | "viewer_stopped"
  | string;

export interface MirrorEvent {
  readonly id: string;
  readonly runtime_id: string;
  readonly runtime_name: string;
  readonly event: MirrorEventType;
  readonly failure_reason?: string;
  readonly viewer_id?: string;
  readonly created_at: string;
}

export interface MirrorEventsResponse {
  readonly events: readonly MirrorEvent[];
}
