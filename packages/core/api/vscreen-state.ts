import { z } from "zod";
import type {
  VscreenCommandReceipt,
  VscreenEnvelope,
  VscreenScope,
  VscreenSourcesResponse,
  VscreenStateResponse,
} from "../types/vscreen";
import { parseWithFallback } from "./schema";
import {
  VscreenEnvelopeSchema,
  VscreenEpochSchema,
  VscreenIdentitySchema,
  VscreenRevisionSchema,
  VscreenSourceDescriptorSchema,
} from "./vscreen-schemas";

const id = VscreenIdentitySchema;
const permission = z
  .enum(["unknown", "granted", "denied", "restricted", "not_determined"])
  .catch("unknown");
const snapshotSchema = z
  .object({
    runtime_id: id,
    state: z
      .enum([
        "disabled",
        "creating",
        "ready",
        "suspended",
        "stopping",
        "unavailable",
        "unknown",
      ])
      .catch("unknown"),
    native_epoch: z.string(),
    display_generation: z.string(),
    geometry_revision: z
      .number()
      .int()
      .nonnegative()
      .max(Number.MAX_SAFE_INTEGER),
    control_state: z
      .enum([
        "idle",
        "agent",
        "awaiting_takeover",
        "human",
        "stopping",
        "unknown",
      ])
      .catch("unknown"),
    active_task_id: id.nullable(),
    intervention_id: id.nullable(),
    permissions: z
      .object({ screen_recording: permission, accessibility: permission })
      .default({ screen_recording: "unknown", accessibility: "unknown" }),
    human_interaction: z.boolean().default(false),
    state_revision: VscreenRevisionSchema,
  })
  .refine((wire) => {
    const activeEpoch = VscreenEpochSchema.safeParse(wire).success;
    const needsEpoch = ["ready", "suspended", "stopping"].includes(wire.state);
    return (
      (!needsEpoch || activeEpoch) &&
      (wire.control_state !== "idle" || wire.active_task_id === null) &&
      (wire.control_state !== "agent" ||
        (wire.state === "ready" && wire.active_task_id !== null)) &&
      (!["human", "awaiting_takeover"].includes(wire.control_state) ||
        wire.intervention_id !== null)
    );
  })
  .transform((wire) => ({
    runtimeId: wire.runtime_id,
    state: wire.state,
    nativeEpoch: wire.native_epoch,
    displayGeneration: wire.display_generation,
    geometryRevision: wire.geometry_revision,
    controlState: wire.control_state,
    activeTaskId: wire.active_task_id,
    interventionId: wire.intervention_id,
    permissions: {
      screenRecording: wire.permissions.screen_recording,
      accessibility: wire.permissions.accessibility,
    },
    humanInteraction: wire.human_interaction,
    stateRevision: wire.state_revision,
  }));

const stateSchema = z.intersection(
  VscreenEnvelopeSchema,
  z.object({ state: snapshotSchema }),
);
const sourcesSchema = z.intersection(
  VscreenEnvelopeSchema,
  z.object({
    sources: z.array(VscreenSourceDescriptorSchema).max(64).default([]),
  }),
);
const receiptSchema = z.intersection(
  VscreenEnvelopeSchema,
  z
    .object({
      command_id: id,
      receipt_id: id,
      state: z.enum(["pending", "running", "succeeded", "failed", "unknown"]),
      reason: id.optional(),
      epoch: VscreenEpochSchema.optional(),
    })
    .refine((wire) =>
      ["failed", "unknown"].includes(wire.state)
        ? wire.reason !== undefined
        : wire.reason === undefined,
    )
    .transform((wire) => ({
      commandId: wire.command_id,
      receiptId: wire.receipt_id,
      state: wire.state,
      reason: wire.reason,
      epoch: wire.epoch,
    })),
);

export function matchesVscreenScope(
  envelope: Pick<VscreenEnvelope, "workspaceId" | "runtimeId">,
  scope: VscreenScope,
): boolean {
  return (
    envelope.workspaceId === scope.workspaceId &&
    envelope.runtimeId === scope.runtimeId
  );
}

export function parseVscreenState(
  raw: unknown,
  scope: VscreenScope,
): VscreenStateResponse | null {
  const result = parseWithFallback<VscreenStateResponse | null>(
    raw,
    stateSchema,
    null,
    { endpoint: "GET /api/runtimes/:runtimeId/vscreen" },
  );
  return result &&
    matchesVscreenScope(result, scope) &&
    result.state.runtimeId === scope.runtimeId
    ? result
    : null;
}

export function parseVscreenSources(
  raw: unknown,
  scope: VscreenScope,
): VscreenSourcesResponse | null {
  const result = parseWithFallback<VscreenSourcesResponse | null>(
    raw,
    sourcesSchema,
    null,
    { endpoint: "GET /api/runtimes/:runtimeId/mirror/sources" },
  );
  if (!result || !matchesVscreenScope(result, scope)) return null;
  const seen = new Set<string>();
  const first = result.sources[0];
  let primaryCount = 0;
  for (const binding of result.sources) {
    const sourceKey = `${binding.source.kind}:${binding.source.sourceId}`;
    if (
      !matchesVscreenScope(binding.resource, scope) ||
      binding.resource.backendIdentity !== scope.backendIdentity ||
      binding.resource.uid !== first?.resource.uid ||
      binding.nativeEpoch !== first?.nativeEpoch ||
      seen.has(sourceKey)
    )
      return null;
    seen.add(sourceKey);
    if (binding.primary) primaryCount++;
  }
  return primaryCount <= 1 ? result : null;
}

export function parseVscreenReceipt(
  raw: unknown,
  scope: VscreenScope,
  commandId: string,
): VscreenCommandReceipt | null {
  const result = parseWithFallback<VscreenCommandReceipt | null>(
    raw,
    receiptSchema,
    null,
    { endpoint: "/api/runtimes/:runtimeId/vscreen/commands" },
  );
  return result &&
    matchesVscreenScope(result, scope) &&
    result.commandId === commandId
    ? result
    : null;
}
