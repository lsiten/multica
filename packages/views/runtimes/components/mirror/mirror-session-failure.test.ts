// @vitest-environment node

import { describe, expect, it } from "vitest";
import type { MirrorSessionResponse } from "@multica/core/types";
import { mirrorSessionFailureReason } from "./mirror-session-failure";

function session(
  patch: Pick<MirrorSessionResponse, "state"> & Partial<Pick<MirrorSessionResponse, "answer" | "failure_reason">>,
) {
  return patch;
}

describe("mirrorSessionFailureReason", () => {
  it("maps daemon answer failures to typed transport reasons", () => {
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "permission-denied" }))).toBe("permission-denied");
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "unsupported" }))).toBe("unsupported");
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "no-display" }))).toBe("no-display");
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "capture-unavailable" }))).toBe("capture-unavailable");
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "negotiation-failed" }))).toBe("negotiation-failed");
    expect(mirrorSessionFailureReason(session({ state: "failed", failure_reason: "future-reason" }))).toBe("transport");
  });

  it("fails immediately when an answered session has no answer", () => {
    expect(mirrorSessionFailureReason(session({ state: "answered" }))).toBe("transport");
    expect(mirrorSessionFailureReason(session({ state: "answered", answer: { type: "answer", sdp: "sdp" } }))).toBeNull();
  });

  it("maps closed and expired sessions", () => {
    expect(mirrorSessionFailureReason(session({ state: "closed" }))).toBe("session-expired");
    expect(mirrorSessionFailureReason(session({ state: "expired" }))).toBe("session-expired");
    expect(mirrorSessionFailureReason(session({ state: "offered" }))).toBeNull();
  });
});
