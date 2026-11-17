import { runGuardedNavigation } from "@multica/core/platform";
import type { TabSession, TabStore, WorkspaceTabGroup } from "./tab-store";

function activeTab(state: TabStore): TabSession | undefined {
  const group = state.activeWorkspaceSlug ? state.byWorkspace[state.activeWorkspaceSlug] : undefined;
  return group?.tabs.find((tab) => tab.id === group.activeTabId);
}

function perform(guard: boolean, action: () => void): void {
  if (guard) runGuardedNavigation(action);
  else action();
}

/** Guard user actions that unmount the active host; server/session cleanup stays authoritative. */
export function guardTabActions(
  actions: TabStore,
  get: () => TabStore,
  findTab: (group: WorkspaceTabGroup, url: string) => TabSession | undefined,
): TabStore {
  function closesActive(targetId: string, scope: "others" | "right"): boolean {
    const state = get();
    const group = state.activeWorkspaceSlug ? state.byWorkspace[state.activeWorkspaceSlug] : undefined;
    const active = activeTab(state);
    if (!group || !active || active.pinned || active.id === targetId) return false;
    const targetIndex = group.tabs.findIndex((tab) => tab.id === targetId);
    return targetIndex >= 0 && (scope === "others" || group.tabs.indexOf(active) > targetIndex);
  }
  function history(index: number): void {
    const active = activeTab(get());
    const changes = !!active && Number.isInteger(index) && index >= 0 && index < active.history.stack.length && index !== active.history.index;
    perform(changes, () => actions.goToHistoryIndex(index));
  }
  return {
    ...actions,
    switchWorkspace(slug, path) {
      const state = get();
      const active = activeTab(state);
      const group = state.byWorkspace[slug];
      const changes = !!slug && !!active && (slug !== state.activeWorkspaceSlug || (!!path && (!group || findTab(group, path)?.id !== active.id)));
      perform(changes, () => actions.switchWorkspace(slug, path));
    },
    openTab(path, title, options) {
      const state = get();
      const active = activeTab(state);
      const group = state.activeWorkspaceSlug ? state.byWorkspace[state.activeWorkspaceSlug] : undefined;
      const changes = options?.activate === true && !!active && !!group && findTab(group, path)?.id !== active.id;
      let result = "";
      perform(changes, () => { result = actions.openTab(path, title, options); });
      return result;
    },
    setActiveTab(id) {
      const state = get();
      const active = activeTab(state);
      const exists = Object.values(state.byWorkspace).some((group) => group.tabs.some((tab) => tab.id === id));
      perform(!!active && active.id !== id && exists, () => actions.setActiveTab(id));
    },
    closeTab(id) {
      perform(activeTab(get())?.id === id, () => actions.closeTab(id));
    },
    closeOtherTabs(id) {
      perform(closesActive(id, "others"), () => actions.closeOtherTabs(id));
    },
    closeTabsToRight(id) {
      perform(closesActive(id, "right"), () => actions.closeTabsToRight(id));
    },
    navigateActiveSession(url, options) {
      const active = activeTab(get());
      perform(!!active && active.url !== url, () => actions.navigateActiveSession(url, options));
    },
    goBack() {
      const active = activeTab(get());
      if (active) history(active.history.index - 1);
    },
    goForward() {
      const active = activeTab(get());
      if (active) history(active.history.index + 1);
    },
    goToHistoryIndex: history,
    reloadActiveTab() {
      perform(!!activeTab(get()), actions.reloadActiveTab);
    },
  };
}
