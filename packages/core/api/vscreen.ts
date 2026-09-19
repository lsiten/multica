import { createVscreenInterventionsApi } from "./vscreen-interventions";
import type {
  MirrorControlGrant,
  MirrorSource,
  RuntimeMirrorControlState,
  MirrorSourceBinding,
  VscreenCommand,
  VscreenMirrorRequest,
  VscreenScope,
  VscreenViewerSession,
} from "../types/vscreen";
import { parseWithFallback } from "./schema";
import { createVscreenLegacyApi } from "./vscreen-legacy";
import { MirrorICEConfigSchema } from "./schemas";
import type { MirrorICEConfig } from "../types/mirror";
import {
  parseMirrorViewerGrant,
  parseVscreenMirrorSession,
} from "./vscreen-mirror";
import {
  MirrorControlGrantSchema,
  RuntimeMirrorControlStateSchema,
  VscreenCommandSchema,
  VscreenReasonSchema,
  VscreenScopeSchema,
} from "./vscreen-schemas";
import {
  matchesVscreenScope,
  parseVscreenReceipt,
  parseVscreenSources,
  parseVscreenState,
} from "./vscreen-state";

export class VscreenScopeError extends Error {
  constructor() {
    super("Vscreen request identity is no longer current");
    this.name = "VscreenScopeError";
  }
}

export class VscreenContractError extends Error {
  constructor() {
    super("Vscreen response does not match the requested contract");
    this.name = "VscreenContractError";
  }
}

export function vscreenErrorReason(error: unknown): string | null {
  if (!(error instanceof Error) || !("body" in error)) return null;
  return parseWithFallback<string | null>(
    error.body,
    VscreenReasonSchema,
    null,
    { endpoint: "vscreen error" },
  );
}

type VscreenTransport = (path: string, init: RequestInit) => Promise<Response>;

/** Scope and transport are captured when query options are built, never at retry time. */
export function createVscreenApi(
  scope: VscreenScope,
  transport: VscreenTransport,
  isCurrent: () => boolean,
) {
  const identity = VscreenScopeSchema.parse(scope);
  const root = `/api/runtimes/${encodeURIComponent(identity.runtimeId)}`;
  async function request(
    path: string,
    init: RequestInit = {},
  ): Promise<unknown> {
    if (!isCurrent()) throw new VscreenScopeError();
    const response = await transport(path.startsWith("/api/") ? path : `${root}${path}`, {
      ...init,
      signal: init.signal
        ? AbortSignal.any([init.signal, AbortSignal.timeout(15_000)])
        : AbortSignal.timeout(15_000),
      headers: {
        "Content-Type": "application/json",
        "X-Workspace-ID": identity.workspaceId,
        "X-Workspace-Slug": "",
      },
    });
    if (response.status === 204) return null;
    try {
      const raw: unknown = await response.json();
      if (!isCurrent()) throw new VscreenScopeError();
      return raw;
    } catch (error) {
      if (error instanceof SyntaxError) throw new VscreenContractError();
      throw error;
    }
  }
  function requireBinding(binding: MirrorSourceBinding): void {
    if (
      !matchesVscreenScope(binding.resource, identity) ||
      binding.resource.backendIdentity !== identity.backendIdentity
    )
      throw new VscreenScopeError();
  }
  return {
    legacy: createVscreenLegacyApi(identity, request),
    interventions: createVscreenInterventionsApi(identity, request),
    async getIceConfig(signal?: AbortSignal) {
      const result = parseWithFallback<MirrorICEConfig | null>(
        await request("/mirror/config", { signal }),
        MirrorICEConfigSchema,
        null,
        { endpoint: "GET /api/runtimes/:runtimeId/mirror/config" },
      );
      return result
        ? {
            iceServers: result.ice_servers,
            turnConfigured: result.turn_configured,
          }
        : null;
    },
    async getState(signal?: AbortSignal) {
      return parseVscreenState(await request("/vscreen", { signal }), identity);
    },
    async getSources(signal?: AbortSignal) {
      return parseVscreenSources(
        await request("/mirror/sources", { signal }),
        identity,
      );
    },
    async createCommand(command: VscreenCommand) {
      const input = VscreenCommandSchema.parse(command);
      return parseVscreenReceipt(
        await request("/vscreen/commands", {
          method: "POST",
          body: JSON.stringify({
            command_id: input.commandId,
            kind: input.kind,
          }),
        }),
        identity,
        input.commandId,
      );
    },
    async getCommand(commandId: string, signal?: AbortSignal) {
      return parseVscreenReceipt(
        await request(`/vscreen/commands/${encodeURIComponent(commandId)}`, {
          signal,
        }),
        identity,
        commandId,
      );
    },
    async createMirrorSession(input: VscreenMirrorRequest) {
      requireBinding(input.binding);
      const raw = await request("/mirror/sessions", {
        method: "POST",
        body: JSON.stringify({
          viewer_id: input.viewerId,
          offer: input.offer,
          protocol_version: 2,
          transport: "video",
          source: {
            kind: input.binding.source.kind,
            source_id: input.binding.source.sourceId,
          },
          source_generation: input.binding.generation,
        }),
      });
      return parseVscreenMirrorSession(raw, identity, input);
    },
    async getMirrorSession(viewer: VscreenViewerSession, signal?: AbortSignal) {
      requireBinding(viewer.binding);
      return parseVscreenMirrorSession(
        await request(
          `/mirror/sessions/${encodeURIComponent(viewer.sessionId)}`,
          { signal },
        ),
        identity,
        viewer,
      );
    },
    async renewMirrorSession(viewer: VscreenViewerSession) {
      requireBinding(viewer.binding);
      return parseMirrorViewerGrant(
        await request(
          `/mirror/sessions/${encodeURIComponent(viewer.sessionId)}/renew`,
          { method: "POST" },
        ),
        identity,
        viewer,
      );
    },
    async closeMirrorSession(viewer: VscreenViewerSession) {
      requireBinding(viewer.binding);
      await request(
        `/mirror/sessions/${encodeURIComponent(viewer.sessionId)}?viewer_id=${encodeURIComponent(viewer.viewerId)}`,
        { method: "DELETE", keepalive: true },
      );
    },
    async createMirrorControlGrant(
      viewer: Pick<VscreenViewerSession, "viewerId">,
      source: MirrorSource,
      sourceGeneration: string,
      signal?: AbortSignal,
    ): Promise<MirrorControlGrant> {
      const raw = await request("/mirror/control-grants", {
        method: "POST",
        signal,
        body: JSON.stringify({
          viewer_id: viewer.viewerId,
          source: {
            kind: source.kind,
            source_id: source.sourceId,
          },
          source_generation: sourceGeneration,
        }),
      });
      return parseMirrorControlGrant(raw, identity);
    },
    async getMirrorControlState(signal?: AbortSignal): Promise<RuntimeMirrorControlState> {
      const raw = await request("/mirror/control-state", { signal });
      const state = parseWithFallback<RuntimeMirrorControlState | null>(
        raw,
        RuntimeMirrorControlStateSchema,
        null,
        { endpoint: "GET /api/runtimes/:runtimeId/mirror/control-state" },
      );
      if (
        !state ||
        state.workspaceId !== identity.workspaceId ||
        state.runtimeId !== identity.runtimeId
      ) {
        throw new VscreenContractError();
      }
      return state;
    },

    async renewMirrorControlGrant(
      viewer: Pick<VscreenViewerSession, "viewerId">,
      source: MirrorSource,
      sourceGeneration: string,
      signal?: AbortSignal,
    ): Promise<MirrorControlGrant> {
      const raw = await request("/mirror/control-grants/renew", {
        method: "POST",
        signal,
        body: JSON.stringify({
          viewer_id: viewer.viewerId,
          source: {
            kind: source.kind,
            source_id: source.sourceId,
          },
          source_generation: sourceGeneration,
        }),
      });
      return parseMirrorControlGrant(raw, identity);
    },
    async revokeMirrorControlGrant(viewerId: string): Promise<void> {
      await request(
        `/mirror/control-grants/${encodeURIComponent(viewerId)}`,
        { method: "DELETE" },
      );
    },
  };
}

function parseMirrorControlGrant(
  raw: unknown,
  scope: VscreenScope,
): MirrorControlGrant {
  const grant = parseWithFallback<MirrorControlGrant | null>(
    raw,
    MirrorControlGrantSchema,
    null,
    { endpoint: "POST /api/runtimes/:runtimeId/mirror/control-grants" },
  );
  if (
    !grant ||
    grant.workspaceId !== scope.workspaceId ||
    grant.runtimeId !== scope.runtimeId ||
    grant.userId !== scope.accountId ||
    Date.parse(grant.expiresAt) <= Date.now()
  ) {
    throw new VscreenContractError();
  }
  return grant;
}


export type VscreenApi = ReturnType<typeof createVscreenApi>;
