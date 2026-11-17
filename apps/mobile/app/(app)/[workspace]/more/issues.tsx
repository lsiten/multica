import { useMemo } from "react";
import { View } from "react-native";
import { useInfiniteQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { ScopeToolbar, ActiveFilterChips } from "@/components/issue/issue-list-controls";
import { PaginatedIssueList } from "@/components/issue/paginated-issue-list";
import { issueListOptions } from "@/data/queries/issues";
import type { IssueListFilter } from "@/data/queries/issue-pages";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useIssuesViewStore, type IssuesScope } from "@/data/stores/issues-view-store";
import { useClearFiltersOnWorkspaceChange } from "@/lib/use-clear-filters-on-workspace-change";
import { useT } from "@/lib/i18n";
import { useIssueStatuses } from "@/lib/use-issue-statuses";

const SCOPES: readonly IssuesScope[] = ["all", "members", "agents"];

export default function IssuesPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("issues");
  const scope = useIssuesViewStore((s) => s.scope);
  const setScope = useIssuesViewStore((s) => s.setScope);
  const statusFilters = useIssuesViewStore((s) => s.statusFilters);
  const priorityFilters = useIssuesViewStore((s) => s.priorityFilters);
  const catalog = useIssueStatuses();

  useClearFiltersOnWorkspaceChange(useIssuesViewStore.getState().clearFilters, wsId);
  const filter = useMemo<IssueListFilter>(() => ({
    statuses: statusFilters,
    priorities: priorityFilters,
    ...(scope === "members" ? { assignee_types: ["member"] } : scope === "agents" ? { assignee_types: ["agent", "squad"] } : {}),
  }), [statusFilters, priorityFilters, scope]);
  const query = useInfiniteQuery(issueListOptions(wsId, filter));
  const hasActiveFilters = statusFilters.length > 0 || priorityFilters.length > 0;
  const openFilter = () => {
    if (wsSlug) router.push({ pathname: "/[workspace]/issues-filter", params: { workspace: wsSlug, scope: "all" } });
  };

  return <View className="flex-1 bg-background">
    <ScopeToolbar scopes={SCOPES.map((value) => ({ value, label: t(`tabs.${value}`) }))} scope={scope} onChange={setScope} onOpenFilter={openFilter} hasActiveFilters={hasActiveFilters} />
    {hasActiveFilters && <ActiveFilterChips
      statusFilters={statusFilters} priorityFilters={priorityFilters} statusLabelOf={catalog.labelOf}
      onClearStatus={(status) => useIssuesViewStore.getState().toggleStatusFilter(status)}
      onClearPriority={(priority) => useIssuesViewStore.getState().togglePriorityFilter(priority)}
    />}
    <PaginatedIssueList query={query} workspaceSlug={wsSlug} emptyMessage={hasActiveFilters ? t("empty.filtered") : t(`empty.${scope}`)} />
  </View>;
}
