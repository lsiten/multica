import { describe, expect, it } from "vitest";
import { canContinueChatFailure } from "./failure-recovery";

describe("canContinueChatFailure", () => {
  it("allows continuation for transient provider and runtime failures", () => {
    expect(canContinueChatFailure("runtime_offline")).toBe(true);
    expect(canContinueChatFailure("agent_error.provider_network")).toBe(true);
    expect(canContinueChatFailure("agent_error.provider_capacity_or_rate_limit")).toBe(true);
  });

  it("keeps poisoned or unknown failures on retry only", () => {
    expect(canContinueChatFailure("api_invalid_request")).toBe(false);
    expect(canContinueChatFailure("agent_error.context_overflow")).toBe(false);
    expect(canContinueChatFailure("agent_error.unknown")).toBe(false);
    expect(canContinueChatFailure(undefined)).toBe(false);
  });
});
