import { infiniteQueryOptions, type InfiniteData } from "@tanstack/react-query";
import type { Issue, ListIssuesParams, ListIssuesResponse } from "@multica/core/types";
import { api } from "@/data/api";
import { issueKeys } from "./issue-keys";

export type IssueListFilter = Omit<ListIssuesParams, "offset" | "limit">;

export const issueListOptions = (wsId: string | null, filter: IssueListFilter = {}) =>
  infiniteQueryOptions({
    queryKey: [...issueKeys.pages(wsId), filter] as const,
    initialPageParam: 0,
    queryFn: ({ signal, pageParam }) => api.listIssues({ ...filter, limit: 100, offset: pageParam }, { signal }),
    getNextPageParam: (lastPage, pages) => {
      const loaded = pages.reduce((count, page) => count + page.issues.length, 0);
      return lastPage.issues.length > 0 && loaded < lastPage.total ? loaded : undefined;
    },
    enabled: !!wsId,
  });

export function loadedIssues(data: InfiniteData<ListIssuesResponse> | undefined): Issue[] {
  const byId = new Map<string, Issue>();
  for (const page of data?.pages ?? []) {
    for (const issue of page.issues) byId.set(issue.id, issue);
  }
  return [...byId.values()];
}
