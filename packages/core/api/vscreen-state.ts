import { z } from "zod";
import type {
  VscreenEnvelope,
  VscreenScope,
  VscreenSourcesResponse,
} from "../types/vscreen";
import { parseWithFallback } from "./schema";
import {
  VscreenEnvelopeSchema,
  VscreenSourceDescriptorSchema,
} from "./vscreen-schemas";

const sourcesSchema = z.intersection(
  VscreenEnvelopeSchema,
  z.object({
    sources: z.array(VscreenSourceDescriptorSchema).max(64).default([]),
  }),
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
