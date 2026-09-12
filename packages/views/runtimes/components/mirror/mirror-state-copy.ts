import type { useT } from "../../../i18n";
import { assertNever } from "./assert-never";
import type { RuntimeMirrorPageState } from "./mirror-page-state";

export interface MirrorStateCopy {
  readonly title: string;
  readonly description: string;
  readonly status: string;
}

export function mirrorStateCopy(
  t: ReturnType<typeof useT<"runtimes">>["t"],
  state: RuntimeMirrorPageState,
): MirrorStateCopy {
  switch (state) {
    case "loading":
      return copy(t, "loading");
    case "preparing":
      return copy(t, "preparing");
    case "negotiating":
      return copy(t, "negotiating");
    case "streaming":
      return copy(t, "streaming");
    case "offline":
      return copy(t, "offline");
    case "old-daemon":
      return copy(t, "old_daemon");
    case "linux-unsupported":
      return copy(t, "linux_unsupported");
    case "unsupported-platform":
      return copy(t, "unsupported_platform");
    case "permission-denied":
      return copy(t, "permission_denied");
    case "capture-unavailable":
      return copy(t, "capture_unavailable");
    case "transport-failed":
      return copy(t, "transport_failed");
    case "unavailable":
      return copy(t, "unavailable");
    default:
      assertNever(state);
  }
}

function copy(
  t: ReturnType<typeof useT<"runtimes">>["t"],
  key:
    | "loading"
    | "preparing"
    | "negotiating"
    | "streaming"
    | "offline"
    | "old_daemon"
    | "linux_unsupported"
    | "unsupported_platform"
    | "permission_denied"
    | "capture_unavailable"
    | "transport_failed"
    | "unavailable",
): MirrorStateCopy {
  return {
    title: t(($) => $.mirror.states[key].title),
    description: t(($) => $.mirror.states[key].description),
    status: t(($) => $.mirror.states[key].status),
  };
}
