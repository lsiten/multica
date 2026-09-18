import { z } from "zod";
import type {
  MirrorSourceBinding,
  MirrorViewerGrant,
  VscreenMirrorSession,
  VscreenScope,
  VscreenVideoMetadata,
  VscreenViewerSession,
} from "../types/vscreen";
import { parseWithFallback } from "./schema";
import {
  MirrorSourceBindingSchema,
  MirrorViewerGrantSchema,
  VscreenIdentitySchema,
  VscreenRevisionSchema,
  VscreenVideoQualitySchema,
} from "./vscreen-schemas";
import { matchesVscreenScope } from "./vscreen-state";

const id = VscreenIdentitySchema;
const sessionSchema = z
  .object({
    id,
    workspace_id: id,
    runtime_id: id,
    user_id: id,
    daemon_id: id,
    viewer_id: id,
    created_at: z.iso.datetime({ offset: true }),
    expires_at: z.iso.datetime({ offset: true }),
    state: z
      .enum(["offered", "answered", "failed", "closed", "expired", "unknown"])
      .catch("unknown"),
    failure_reason: z.string().optional(),
    answer: z
      .object({ type: z.literal("answer"), sdp: z.string().min(1) })
      .optional(),
    ice_config: z.object({
      ice_servers: z.array(
        z.object({
          urls: z.union([z.string().min(1), z.array(z.string().min(1))]),
          username: z.string().optional(),
          credential: z.string().optional(),
        }),
      ),
      turn_configured: z.boolean(),
    }),
    viewer_grant: MirrorViewerGrantSchema.optional(),
    video_quality: VscreenVideoQualitySchema.optional(),
  })
  .transform((wire) => ({
    id: wire.id,
    workspaceId: wire.workspace_id,
    runtimeId: wire.runtime_id,
    userId: wire.user_id,
    daemonId: wire.daemon_id,
    viewerId: wire.viewer_id,
    createdAt: wire.created_at,
    expiresAt: wire.expires_at,
    state: wire.state,
    failureReason: wire.failure_reason,
    answer: wire.answer,
    iceConfig: {
      iceServers: wire.ice_config.ice_servers,
      turnConfigured: wire.ice_config.turn_configured,
    },
    viewerGrant: wire.viewer_grant,
    videoQuality: wire.video_quality,
  }));

const videoMetadataSchema = z
  .object({
    type: z.literal("mirror:video-meta"),
    source_binding: MirrorSourceBindingSchema,
    geometry_revision: VscreenRevisionSchema,
    pts_nanos: z.number().nonnegative(),
    quality: VscreenVideoQualitySchema,
  })
  .transform((wire) => ({
    type: wire.type,
    sourceBinding: wire.source_binding,
    geometryRevision: wire.geometry_revision,
    ptsNanos: wire.pts_nanos,
    quality: wire.quality,
  }));

export function matchesMirrorBinding(
  actual: MirrorSourceBinding,
  expected: MirrorSourceBinding,
): boolean {
  return (
    actual.resource.backendIdentity === expected.resource.backendIdentity &&
    actual.resource.workspaceId === expected.resource.workspaceId &&
    actual.resource.runtimeId === expected.resource.runtimeId &&
    actual.resource.uid === expected.resource.uid &&
    actual.source.kind === expected.source.kind &&
    actual.source.sourceId === expected.source.sourceId &&
    actual.nativeEpoch === expected.nativeEpoch &&
    actual.generation === expected.generation
  );
}

function matchesGrant(
  grant: MirrorViewerGrant,
  viewer: VscreenViewerSession,
): boolean {
  return (
    Date.parse(grant.expiresAt) > Date.now() &&
    grant.sessionId === viewer.sessionId &&
    grant.viewerId === viewer.viewerId &&
    grant.nativeEpoch === viewer.binding.nativeEpoch &&
    grant.source.kind === viewer.binding.source.kind &&
    grant.source.sourceId === viewer.binding.source.sourceId &&
    grant.sourceGeneration === viewer.binding.generation
  );
}

export function parseMirrorViewerGrant(
  raw: unknown,
  scope: VscreenScope,
  viewer: VscreenViewerSession,
): MirrorViewerGrant | null {
  const grant = parseWithFallback<MirrorViewerGrant | null>(
    raw,
    MirrorViewerGrantSchema,
    null,
    {
      endpoint:
        "POST /api/runtimes/:runtimeId/mirror/sessions/:sessionId/renew",
    },
  );
  return grant &&
    matchesVscreenScope(grant, scope) &&
    grant.userId === scope.accountId &&
    matchesGrant(grant, viewer)
    ? grant
    : null;
}

export function parseVscreenMirrorSession(
  raw: unknown,
  scope: VscreenScope,
  viewer: Omit<VscreenViewerSession, "sessionId"> & {
    readonly sessionId?: string;
  },
): VscreenMirrorSession | null {
  const session = parseWithFallback<VscreenMirrorSession | null>(
    raw,
    sessionSchema,
    null,
    { endpoint: "/api/runtimes/:runtimeId/mirror/sessions" },
  );
  if (
    !session ||
    !matchesVscreenScope(session, scope) ||
    session.userId !== scope.accountId ||
    session.viewerId !== viewer.viewerId ||
    (viewer.sessionId !== undefined && session.id !== viewer.sessionId)
  )
    return null;
  const grant = session.viewerGrant;
  if (
    !grant ||
    !matchesVscreenScope(grant, scope) ||
    grant.userId !== scope.accountId ||
    !matchesGrant(grant, { ...viewer, sessionId: session.id })
  )
    return null;
  return session;
}

export function parseVscreenVideoMetadata(
  raw: unknown,
  expected: {
    readonly binding: MirrorSourceBinding;
    readonly geometryRevision: number;
  },
): VscreenVideoMetadata | null {
  const metadata = parseWithFallback<VscreenVideoMetadata | null>(
    raw,
    videoMetadataSchema,
    null,
    { endpoint: "mirror:video-meta" },
  );
  return metadata &&
    matchesMirrorBinding(metadata.sourceBinding, expected.binding) &&
    metadata.geometryRevision === expected.geometryRevision
    ? metadata
    : null;
}
