import { useEffect, useState } from "react";
import { useInfiniteQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { readWorkspaceSearchPage, type DirectorySearchCursor, WORKSPACE_DIRECTORY_READ_TIMEOUT_MS } from "./workspace-directory-query";

const SEARCH_BATCH_PAGES = 20;
const INITIAL_CURSOR: DirectorySearchCursor = { path: ".", offset: 0 };

export function useWorkspaceFileSearch(request: PagedReviewInput | null, directoryKey: QueryKey, term: string, enabled: boolean) {
  const client = useQueryClient();
  const queryKey = [...directoryKey, "search", term];
  const key = JSON.stringify(queryKey);
  const [budget, setBudget] = useState({ key: "", pages: SEARCH_BATCH_PAGES });
  const pageLimit = budget.key === key ? budget.pages : SEARCH_BATCH_PAGES;
  const search = useInfiniteQuery({
    queryKey,
    initialPageParam: INITIAL_CURSOR,
    queryFn: ({ pageParam, signal }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      return readWorkspaceSearchPage(request, term, pageParam, AbortSignal.any([signal, AbortSignal.timeout(WORKSPACE_DIRECTORY_READ_TIMEOUT_MS)]));
    },
    getNextPageParam: (page) => page.next,
    enabled: enabled && !!request && !!term,
    structuralSharing: false,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  });
  const { fetchNextPage, hasNextPage, isFetching, isError } = search;
  const pageCount = search.data?.pages.length ?? 0;
  const paused = !!hasNextPage && pageCount >= pageLimit;
  useEffect(() => {
    if (!enabled || !term || !hasNextPage || isFetching || isError || paused) return;
    // Yield between pages so results paint and input/collapse remains responsive.
    const timer = setTimeout(() => void fetchNextPage(), 25);
    return () => clearTimeout(timer);
  }, [enabled, term, hasNextPage, isFetching, isError, fetchNextPage, pageCount, paused]);
  useEffect(() => {
    if (enabled) return;
    void client.cancelQueries({ queryKey: JSON.parse(key), exact: true });
  }, [client, enabled, key]);
  return {
    ...search,
    entries: search.data?.pages.flatMap((page) => page.entries) ?? [],
    paused,
    continueSearch: () => setBudget({ key, pages: pageCount + SEARCH_BATCH_PAGES }),
    searching: enabled && !!request && !!term && !isError && (search.isPending || isFetching || (!!hasNextPage && !paused)),
  };
}
