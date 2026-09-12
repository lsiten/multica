import {
  RUNTIME_MIRROR_AVAILABILITY,
  type RuntimeMirrorAvailabilityKind,
} from "@multica/core/runtimes";
import type { RuntimeMirrorFailureReason } from "./mirror-session-failure";
import type { RuntimeMirrorTransportState } from "./use-runtime-mirror-session";

export const RUNTIME_MIRROR_PAGE_STATES = [
  "loading",
  "preparing",
  "negotiating",
  "streaming",
  "offline",
  "old-daemon",
  "linux-unsupported",
  "unsupported-platform",
  "permission-denied",
  "capture-unavailable",
  "transport-failed",
  "unavailable",
] as const;

export type RuntimeMirrorPageState = (typeof RUNTIME_MIRROR_PAGE_STATES)[number];

interface MirrorPageStateInput {
  readonly queryPending: boolean;
  readonly queryError: boolean;
  readonly runtimeFound: boolean;
  readonly canReadRuntime: boolean;
  readonly availability: RuntimeMirrorAvailabilityKind | null;
  readonly transportState: RuntimeMirrorTransportState;
  readonly failureReason: RuntimeMirrorFailureReason | null;
}

function failureState(reason: RuntimeMirrorFailureReason | null): RuntimeMirrorPageState {
  switch (reason) {
    case "permission-denied":
      return "permission-denied";
    case "no-display":
    case "capture-unavailable":
      return "capture-unavailable";
    case "unsupported":
      return "old-daemon";
    default:
      return "transport-failed";
  }
}

export function runtimeMirrorPageState(input: MirrorPageStateInput): RuntimeMirrorPageState {
  if (input.queryPending) return "loading";
  if (input.queryError || !input.runtimeFound || !input.canReadRuntime) return "unavailable";
  switch (input.availability) {
    case RUNTIME_MIRROR_AVAILABILITY.offline:
      return "offline";
    case RUNTIME_MIRROR_AVAILABILITY.oldDaemon:
      return "old-daemon";
    case RUNTIME_MIRROR_AVAILABILITY.linuxUnsupported:
      return "linux-unsupported";
    case RUNTIME_MIRROR_AVAILABILITY.unsupportedPlatform:
      return "unsupported-platform";
    case RUNTIME_MIRROR_AVAILABILITY.supported:
      break;
    case null:
      return "unavailable";
    default:
      return "unavailable";
  }
  if (input.transportState === "failed") return failureState(input.failureReason);
  switch (input.transportState) {
    case "preparing":
      return "preparing";
    case "negotiating":
      return "negotiating";
    case "streaming":
      return "streaming";
    case "idle":
    case "closed":
      return "preparing";
    default:
      return "preparing";
  }
}

export function isTerminalMirrorState(state: RuntimeMirrorPageState): boolean {
  return state === "offline" ||
    state === "old-daemon" ||
    state === "linux-unsupported" ||
    state === "unsupported-platform" ||
    state === "permission-denied" ||
    state === "capture-unavailable" ||
    state === "transport-failed" ||
    state === "unavailable";
}
