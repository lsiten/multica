import type {
  CreateMirrorSessionRequest,
  MirrorICEConfig,
  MirrorSessionResponse,
} from "../types/mirror";
import type { VscreenScope } from "../types/vscreen";
import { parseWithFallback } from "./schema";
import {
  EMPTY_MIRROR_ICE_CONFIG,
  EMPTY_MIRROR_SESSION_RESPONSE,
  MirrorICEConfigSchema,
  MirrorSessionResponseSchema,
} from "./schemas";

/** Preserve v1 wire fields while binding every request to one authenticated viewer scope. */
export function createVscreenLegacyApi(
  scope: VscreenScope,
  request: (path: string, init?: RequestInit) => Promise<unknown>,
) {
  function parseSession(
    raw: unknown,
    viewer: { readonly viewerId: string; readonly sessionId?: string },
  ): MirrorSessionResponse {
    const result = parseWithFallback<MirrorSessionResponse>(
      raw,
      MirrorSessionResponseSchema,
      EMPTY_MIRROR_SESSION_RESPONSE,
      { endpoint: "v1 /mirror/sessions" },
    );
    const grant = result.viewer_grant;
    if (
      !result.id ||
      result.workspace_id !== scope.workspaceId ||
      result.runtime_id !== scope.runtimeId ||
      result.user_id !== scope.accountId ||
      result.viewer_id !== viewer.viewerId ||
      (viewer.sessionId !== undefined && result.id !== viewer.sessionId)
    )
      return EMPTY_MIRROR_SESSION_RESPONSE;
    if (
      grant &&
      (grant.sessionId !== result.id ||
        grant.viewerId !== result.viewer_id ||
        grant.workspaceId !== scope.workspaceId ||
        grant.runtimeId !== scope.runtimeId ||
        grant.userId !== scope.accountId)
    )
      return EMPTY_MIRROR_SESSION_RESPONSE;
    return result;
  }
  return {
    async getIceConfig(): Promise<MirrorICEConfig> {
      try {
        return parseWithFallback<MirrorICEConfig>(
          await request("/mirror/config"),
          MirrorICEConfigSchema,
          EMPTY_MIRROR_ICE_CONFIG,
          { endpoint: "v1 /mirror/config" },
        );
      } catch (error) {
        if (
          error instanceof Error &&
          "status" in error &&
          (error.status === 404 || error.status === 501)
        )
          return EMPTY_MIRROR_ICE_CONFIG;
        throw error;
      }
    },
    async createSession(input: CreateMirrorSessionRequest) {
      return parseSession(
        await request("/mirror/sessions", {
          method: "POST",
          body: JSON.stringify(input),
        }),
        { viewerId: input.viewer_id },
      );
    },
    async getSession(viewer: {
      readonly sessionId: string;
      readonly viewerId: string;
    }) {
      return parseSession(
        await request(
          `/mirror/sessions/${encodeURIComponent(viewer.sessionId)}`,
        ),
        viewer,
      );
    },
    async closeSession(viewer: {
      readonly sessionId: string;
      readonly viewerId: string;
    }) {
      await request(
        `/mirror/sessions/${encodeURIComponent(viewer.sessionId)}?viewer_id=${encodeURIComponent(viewer.viewerId)}`,
        { method: "DELETE", keepalive: true },
      );
    },
  };
}
