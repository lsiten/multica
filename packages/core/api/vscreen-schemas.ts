import { z } from "zod";

export const VscreenIdentitySchema = z
  .string()
  .min(1)
  .max(512)
  .refine((value) => value.trim() === value && !/[\0\r\n]/.test(value));
export const VscreenRevisionSchema = z
  .number()
  .int()
  .positive()
  .max(Number.MAX_SAFE_INTEGER);
export const VscreenBackendIdentitySchema = z.url().refine((value) => {
  const url = new URL(value);
  return (
    ["http:", "https:"].includes(url.protocol) &&
    !url.username &&
    !url.password &&
    !url.search &&
    !url.hash &&
    !value.endsWith("/") &&
    url.origin + (url.pathname === "/" ? "" : url.pathname) === value
  );
});
export const VscreenScopeSchema = z.object({
  backendIdentity: VscreenBackendIdentitySchema,
  accountId: VscreenIdentitySchema,
  workspaceId: VscreenIdentitySchema,
  runtimeId: VscreenIdentitySchema,
});
const id = VscreenIdentitySchema;
const revision = VscreenRevisionSchema;
export const VscreenEnvelopeSchema = z
  .object({
    workspace_id: id,
    runtime_id: id,
    daemon_generation: id,
    request_id: id,
  })
  .transform((wire) => ({
    workspaceId: wire.workspace_id,
    runtimeId: wire.runtime_id,
    daemonGeneration: wire.daemon_generation,
    requestId: wire.request_id,
  }));
export const VscreenEpochSchema = z
  .object({
    native_epoch: id,
    display_generation: id,
    geometry_revision: revision,
  })
  .transform((wire) => ({
    nativeEpoch: wire.native_epoch,
    displayGeneration: wire.display_generation,
    geometryRevision: wire.geometry_revision,
  }));
export const VscreenResourceSchema = z
  .object({
    backend_identity: VscreenBackendIdentitySchema,
    workspace_id: id,
    runtime_id: id,
    uid: z.number().int().positive().max(4294967295),
  })
  .transform((wire) => ({
    backendIdentity: wire.backend_identity,
    workspaceId: wire.workspace_id,
    runtimeId: wire.runtime_id,
    uid: wire.uid,
  }));
export const MirrorSourceSchema = z
  .object({ kind: z.enum(["virtual", "physical", "system"]), source_id: id })
  .transform((wire) => ({ kind: wire.kind, sourceId: wire.source_id }));
export const MirrorSourceBindingSchema = z
  .object({
    resource: VscreenResourceSchema,
    source: MirrorSourceSchema,
    native_epoch: id,
    generation: id,
    primary: z.boolean(),
  })
  .transform((wire) => ({
    resource: wire.resource,
    source: wire.source,
    nativeEpoch: wire.native_epoch,
    generation: wire.generation,
    primary: wire.primary,
  }));
export const VscreenSourceDescriptorSchema = z
  .object({
    resource: VscreenResourceSchema,
    source: MirrorSourceSchema,
    native_epoch: id,
    generation: id,
    primary: z.boolean(),
    name: z.string().max(256),
    width: z.number().int().min(0).max(32768),
    height: z.number().int().min(0).max(32768),
    scale: z.number().min(0).max(16),
  })
  .transform((wire) => ({
    resource: wire.resource,
    source: wire.source,
    nativeEpoch: wire.native_epoch,
    generation: wire.generation,
    primary: wire.primary,
    name: wire.name,
    width: wire.width,
    height: wire.height,
    scale: wire.scale,
  }));

export const MirrorViewerGrantSchema = z
  .object({
    grant_id: id,
    session_id: id,
    workspace_id: id,
    runtime_id: id,
    user_id: id,
    viewer_id: id,
    native_epoch: id,
    source: MirrorSourceSchema,
    source_generation: id,
    expires_at: z.iso.datetime({ offset: true }),
  })
  .transform((wire) => ({
    grantId: wire.grant_id,
    sessionId: wire.session_id,
    workspaceId: wire.workspace_id,
    runtimeId: wire.runtime_id,
    userId: wire.user_id,
    viewerId: wire.viewer_id,
    nativeEpoch: wire.native_epoch,
    source: wire.source,
    sourceGeneration: wire.source_generation,
    expiresAt: wire.expires_at,
  }));

export const VscreenCommandSchema = z.object({
  commandId: id,
  kind: z.enum(["enable", "disable", "request_takeover"]),
});
export const VscreenReasonSchema = z
  .object({ reason: id })
  .transform((wire) => wire.reason);

export const VscreenVideoQualitySchema = z
  .object({
    width: z.number().int().positive().max(1600).multipleOf(2),
    height: z.number().int().positive().max(900).multipleOf(2),
    fps: z.number().int().positive().max(30),
    bitrate: z.number().int().positive().max(20_000_000),
    max_level_idc: z.union([z.literal(31), z.literal(40)]),
  })
  .refine(
    (wire) =>
      wire.max_level_idc === 40 ||
      (wire.width <= 1280 && wire.height <= 720 && wire.bitrate <= 14_000_000),
  )
  .transform((wire) => ({
    width: wire.width,
    height: wire.height,
    fps: wire.fps,
    bitrate: wire.bitrate,
    maxLevelIdc: wire.max_level_idc,
  }));
