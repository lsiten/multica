import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { registerNavigationGuard, runGuardedNavigation } from "@multica/core/platform";
import { getActiveTab, useTabStore } from "./tab-store";

let unregister: (() => void) | undefined;

beforeEach(() => {
  useTabStore.getState().reset();
  useTabStore.getState().switchWorkspace("acme");
  useTabStore.getState().navigateActiveSession("/acme/workflows/draft");
});
afterEach(() => unregister?.());

describe("desktop unsaved navigation", () => {
  it.each([
    "switch", "close", "shortcut close", "close others", "close right", "navigate",
    "workspace", "back", "forward", "history", "reload", "foreground open",
  ])("keeps the mounted draft when %s is declined", (action) => {
    const store = useTabStore.getState();
    const first = getActiveTab(store);
    expect(first).not.toBeNull();
    if (!first) return;
    const other = store.openTab("/acme/projects", "Projects");
    // Put the edited tab to the right of the background target.
    store.moveTab(1, 0);
    if (action === "forward") store.goBack();
    const before = useTabStore.getState();
    const guard = vi.fn(() => false);
    unregister = registerNavigationGuard(guard);
    const actions: Record<string, () => void> = {
      switch: () => store.setActiveTab(other),
      close: () => store.closeTab(first.id),
      "shortcut close": () => store.closeActiveTab(),
      "close others": () => store.closeOtherTabs(other),
      "close right": () => store.closeTabsToRight(other),
      navigate: () => store.navigateActiveSession("/acme/issues"),
      workspace: () => store.switchWorkspace("other"),
      back: () => store.goBack(),
      forward: () => store.goForward(),
      history: () => store.goToHistoryIndex(0),
      reload: () => store.reloadActiveTab(),
      "foreground open": () => { expect(store.openTab("/acme/settings", "Settings", { activate: true })).toBe(""); },
    };
    actions[action]?.();
    expect(guard).toHaveBeenCalledOnce();
    expect(useTabStore.getState()).toBe(before);
  });

  it("keeps background tab and metadata actions available without prompting", () => {
    const store = useTabStore.getState();
    const active = getActiveTab(store);
    const guard = vi.fn(() => false);
    unregister = registerNavigationGuard(guard);
    const background = store.openTab("/acme/projects", "Projects");
    store.updateTab(background, { title: "Updated" });
    store.closeTab(background);
    store.addTab("/acme/issues", "Issues");
    if (active) store.setActiveTab(active.id);
    if (active) store.openTab(active.url, "Current", { activate: true });
    expect(getActiveTab(useTabStore.getState())?.id).toBe(active?.id);
    expect(guard).not.toHaveBeenCalled();
  });

  it("shares one accepted confirmation with a navigation adapter", () => {
    const guard = vi.fn(() => true);
    unregister = registerNavigationGuard(guard);
    runGuardedNavigation(() => useTabStore.getState().switchWorkspace("other"));
    expect(useTabStore.getState().activeWorkspaceSlug).toBe("other");
    expect(guard).toHaveBeenCalledOnce();
  });

  it("allows authoritative session reset and workspace removal", () => {
    const guard = vi.fn(() => false);
    unregister = registerNavigationGuard(guard);
    useTabStore.getState().validateWorkspaceSlugs(new Set());
    expect(useTabStore.getState().activeWorkspaceSlug).toBeNull();
    useTabStore.getState().reset();
    expect(guard).not.toHaveBeenCalled();
  });
});
