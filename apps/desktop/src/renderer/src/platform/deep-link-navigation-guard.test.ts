import { afterEach, expect, it, vi } from "vitest";
import { registerNavigationGuard } from "@multica/core/platform";
import { getActiveTab, useTabStore } from "@/stores/tab-store";
import { routeIssueDeepLinkPath } from "./navigation";

let unregister: (() => void) | undefined;
afterEach(() => { unregister?.(); useTabStore.getState().reset(); });

it.each([false, true])("uses one %s confirmation for the complete issue deep link", (accept) => {
  useTabStore.getState().reset();
  useTabStore.getState().switchWorkspace("acme");
  useTabStore.getState().navigateActiveSession("/acme/workflows/draft");
  const before = useTabStore.getState();
  const guard = vi.fn(() => accept);
  unregister = registerNavigationGuard(guard);
  routeIssueDeepLinkPath("/acme/issue/ABC-123#comment-target");
  expect(guard).toHaveBeenCalledOnce();
  if (accept) expect(getActiveTab(useTabStore.getState())?.url).toBe("/acme/issue/ABC-123#comment-target");
  else expect(useTabStore.getState()).toBe(before);
});
