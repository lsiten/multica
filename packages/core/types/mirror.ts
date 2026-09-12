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
}
