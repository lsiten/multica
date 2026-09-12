import type { MirrorSessionResponse } from "@multica/core/types";

export type RuntimeMirrorFailureReason =
  | "webrtc-unavailable"
  | "unsupported"
  | "offline"
  | "permission-denied"
  | "no-display"
  | "capture-unavailable"
  | "negotiation-failed"
  | "negotiation-timeout"
  | "session-expired"
  | "transport"
  | "unknown";

export function mirrorSessionFailureReason(
  session: Pick<MirrorSessionResponse, "state" | "answer" | "failure_reason">,
): RuntimeMirrorFailureReason | null {
  if (session.state === "failed") {
    return serverFailureReason(session.failure_reason);
  }
  if (session.state === "answered" && !session.answer) {
    return "transport";
  }
  if (session.state === "closed" || session.state === "expired") {
    return "session-expired";
  }
  return null;
}

function serverFailureReason(reason: string | undefined): RuntimeMirrorFailureReason {
  switch (reason) {
    case "permission-denied":
    case "unsupported":
    case "no-display":
    case "capture-unavailable":
      return reason;
    case "negotiation-failed":
      return "negotiation-failed";
    default:
      return "transport";
  }
}
