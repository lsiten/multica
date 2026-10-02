// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { registerNavigationGuard, mayLeaveNavigation, runGuardedNavigation } from "./navigation-guards";

describe("navigation guards", () => {
  it("shares approval with native operations and releases it after the action", () => {
    const guard = vi.fn(() => true);
    const unregister = registerNavigationGuard(guard);
    try {
      runGuardedNavigation(() => { expect(mayLeaveNavigation()).toBe(true); });
      expect(guard).toHaveBeenCalledTimes(1);
      expect(mayLeaveNavigation()).toBe(true);
      expect(guard).toHaveBeenCalledTimes(2);
    } finally { unregister(); }
  });
  it("blocks navigation, permits forced session cleanup, and removes unmounted guards", () => {
    const unregister = registerNavigationGuard(() => false);
    const action = vi.fn();
    try {
      runGuardedNavigation(action);
      expect(action).not.toHaveBeenCalled();
      runGuardedNavigation(action, true);
      expect(action).toHaveBeenCalledTimes(1);
    } finally { unregister(); }
    expect(mayLeaveNavigation()).toBe(true);
  });
});
