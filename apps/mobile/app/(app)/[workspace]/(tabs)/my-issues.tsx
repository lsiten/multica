import { useMemo } from "react";
import { View } from "react-native";
import { useInfiniteQuery } from "@tanstack/react-query";
import { router, useIsFocused } from "expo-router";
import { Header } from "@/components/ui/header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { ScopeToolbar, ActiveFilterChips } from "@/components/issue/issue-list-controls";
import { PaginatedIssueList } from "@/components/issue/paginated-issue-list";
import { buildMyIssuesFilter } from "@/data/queries/my-issues";
import { issueListOptions } from "@/data/queries/issues";
import type { MyIssuesScope } from "@/data/queries/issue-keys";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useMyIssuesViewStore } from "@/data/stores/my-issues-view-store";
import { useClearFiltersOnWorkspaceChange } from "@/lib/use-clear-filters-on-workspace-change";
import { useT } from "@/lib/i18n";
import { useIssueStatuses } from "@/lib/use-issue-statuses";

const SCOPES: readonly MyIssuesScope[] = ["assigned", "created", "agents"];

export default function MyIssues() {
  const focused = useIsFocused();
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("issues");
  const scope = useMyIssuesViewStore((s) => s.scope);
  const setScope = useMyIssuesViewStore((s) => s.setScope);
  const statusFilters = useMyIssuesViewStore((s) => s.statusFilters);
  const priorityFilters = useMyIssuesViewStore((s) => s.priorityFilters);
  const catalog = useIssueStatuses();

  useClearFiltersOnWorkspaceChange(useMyIssuesViewStore.getState().clearFilters, wsId);
  const filter = useMemo(() => ({
    ...(userId ? buildMyIssuesFilter(scope, userId) : { assignee_id: "" }),
    statuses: statusFilters, priorities: priorityFilters,
  }), [scope, userId, statusFilters, priorityFilters]);
  const query = useInfiniteQuery({ ...issueListOptions(wsId, filter), enabled: !!wsId && !!userId });
  const hasActiveFilters = statusFilters.length > 0 || priorityFilters.length > 0;
  const openFilter = () => {
    if (wsSlug) router.push({ pathname: "/[workspace]/issues-filter", params: { workspace: wsSlug, scope: "my" } });
  };

  return <View className="flex-1 bg-background">
    <Header title={t("navigation:tabs.my_issues")} right={<HeaderActions />} />
    <ScopeToolbar scopes={SCOPES.map((value) => ({ value, label: t(`tabs.${value}`) }))} scope={scope} onChange={setScope} onOpenFilter={openFilter} hasActiveFilters={hasActiveFilters} />
    {hasActiveFilters && <ActiveFilterChips
      statusFilters={statusFilters} priorityFilters={priorityFilters} statusLabelOf={catalog.labelOf}
      onClearStatus={(status) => useMyIssuesViewStore.getState().toggleStatusFilter(status)}
      onClearPriority={(priority) => useMyIssuesViewStore.getState().togglePriorityFilter(priority)}
    />}
    <PaginatedIssueList query={query} workspaceSlug={wsSlug} focused={focused} emptyMessage={hasActiveFilters ? t("empty.filtered") : t(`empty.${scope}`)} />
  </View>;
}
