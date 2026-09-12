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
