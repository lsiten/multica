import type { RuntimeDevice } from "../types";
import { isRuntimeUsableForUser } from "./access";

export const SCREEN_MIRROR_CAPABILITY_V1 = "screen-mirror-v1";

export const RUNTIME_MIRROR_AVAILABILITY = {
  supported: "supported",
  offline: "offline",
  oldDaemon: "old_daemon",
  linuxUnsupported: "linux_unsupported",
  unsupportedPlatform: "unsupported_platform",
} as const;

export type RuntimeMirrorAvailabilityKind =
  (typeof RUNTIME_MIRROR_AVAILABILITY)[keyof typeof RUNTIME_MIRROR_AVAILABILITY];

export interface RuntimeMirrorAvailability {
  readonly kind: RuntimeMirrorAvailabilityKind;
  readonly enabled: boolean;
}

function metadataString(metadata: Record<string, unknown>, key: string): string | null {
  const value = metadata[key];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

function metadataCapabilities(metadata: Record<string, unknown>): readonly string[] {
  const value = metadata.capabilities;
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string");
}

export function runtimeMirrorAvailability(
  runtime: RuntimeDevice,
): RuntimeMirrorAvailability {
  if (runtime.status !== "online" || !runtime.daemon_id) {
    return { kind: RUNTIME_MIRROR_AVAILABILITY.offline, enabled: false };
  }
  if (metadataCapabilities(runtime.metadata).includes(SCREEN_MIRROR_CAPABILITY_V1)) {
    return { kind: RUNTIME_MIRROR_AVAILABILITY.supported, enabled: true };
  }
  switch (metadataString(runtime.metadata, "client_os")) {
    case "linux":
      return { kind: RUNTIME_MIRROR_AVAILABILITY.linuxUnsupported, enabled: false };
    case "macos":
    case "windows":
      return { kind: RUNTIME_MIRROR_AVAILABILITY.oldDaemon, enabled: false };
    default:
      return { kind: RUNTIME_MIRROR_AVAILABILITY.unsupportedPlatform, enabled: false };
  }
}

export function selectRuntimeForMirror(
  runtimes: readonly RuntimeDevice[],
  currentUserId: string | null,
): RuntimeDevice | null {
  const readableRuntimes = runtimes.filter((runtime) =>
    isRuntimeUsableForUser(runtime, currentUserId),
  );
  if (readableRuntimes.length === 0) return null;

  const onlineRuntimes = readableRuntimes.filter(
    (runtime) => runtime.status === "online" && !!runtime.daemon_id,
  );
  return (
    onlineRuntimes.find(
      (runtime) => runtimeMirrorAvailability(runtime).enabled,
    ) ??
    onlineRuntimes[0] ??
    readableRuntimes[0] ??
    null
  );
}
