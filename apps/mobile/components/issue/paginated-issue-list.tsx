import { useMemo } from "react";
import { ActivityIndicator, SectionList, View } from "react-native";
import { router } from "expo-router";
import type { UseInfiniteQueryResult, InfiniteData } from "@tanstack/react-query";
import type { ListIssuesResponse } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { groupIssuesByStatus } from "@/lib/group-issues-by-status";
import { loadedIssues } from "@/data/queries/issue-pages";
import { IssuesLoading } from "./issues-loading";
import { IssueRow } from "./issue-row";
import { EmptyState, SectionHeader } from "./issue-list-controls";

interface Props {
  readonly query: UseInfiniteQueryResult<InfiniteData<ListIssuesResponse>, Error>;
  readonly workspaceSlug: string | null;
  readonly emptyMessage: string;
  readonly focused?: boolean;
}

export function PaginatedIssueList({ query, workspaceSlug, emptyMessage, focused = true }: Props) {
  const { t } = useT("issues");
  const catalog = useIssueStatuses();
  const issues = useMemo(() => loadedIssues(query.data), [query.data]);
  const sections = useMemo(() => groupIssuesByStatus(issues, catalog.statuses), [issues, catalog.statuses]);
  const total = query.data?.pages[0]?.total ?? 0;
  const retry = () => { void query.refetch(); };
  const next = () => {
    if (query.hasNextPage && !query.isFetching) void query.fetchNextPage();
  };

  if (query.isLoading) return <IssuesLoading />;
  if (query.isError && (!query.data || issues.length === 0)) return <View className="gap-3 px-4 pt-4">
    <Text accessibilityRole="alert" className="text-sm text-destructive">{t("errors.load_failed", { message: query.error.message })}</Text>
    <Button variant="outline" onPress={retry}><Text>{t("common:actions.retry")}</Text></Button>
  </View>;
  if (issues.length === 0) return <EmptyState message={emptyMessage} />;

  return <SectionList
    sections={sections}
    keyExtractor={(item) => item.id}
    stickySectionHeadersEnabled={false}
    ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />}
    renderSectionHeader={({ section }) => <SectionHeader status={section.status} count={section.data.length} partial={query.hasNextPage} />}
    ListHeaderComponent={<View className="px-4 py-2"><Text className="text-xs text-muted-foreground">{t("list.loaded", { loaded: issues.length, total })}</Text>
      {query.isRefetchError && <View className="gap-2 py-2"><Text accessibilityRole="alert" className="text-sm text-destructive">{t("list.refresh_failed")}</Text><Button variant="outline" onPress={retry}><Text>{t("common:actions.retry")}</Text></Button></View>}
    </View>}
    ListFooterComponent={<View className="items-center gap-2 px-4 py-4">
      {query.isFetchNextPageError && <Text accessibilityRole="alert" className="text-sm text-destructive">{t("list.next_page_failed")}</Text>}
      {query.hasNextPage && <Button variant="outline" onPress={next} disabled={query.isFetching}>
        {query.isFetchingNextPage && <ActivityIndicator />}
        <Text>{query.isFetchingNextPage ? t("common:states.loading") : query.isFetchNextPageError ? t("common:actions.retry") : t("list.load_more")}</Text>
      </Button>}
    </View>}
    contentContainerClassName="pb-6"
    renderItem={({ item }) => <IssueRow issue={item} onPress={() => { if (workspaceSlug) router.push(`/${workspaceSlug}/issue/${item.id}`); }} />}
    refreshing={focused && query.isRefetching && !query.isFetchingNextPage}
    onRefresh={retry}
    onEndReached={() => { if (!query.isFetchNextPageError) next(); }}
    onEndReachedThreshold={0.5}
  />;
}
