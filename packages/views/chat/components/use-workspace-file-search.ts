import { useEffect } from "react";
import { useInfiniteQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { readWorkspaceSearchPage, type DirectorySearchCursor } from "./workspace-directory-query";

export function useWorkspaceFileSearch(request: PagedReviewInput | null, directoryKey: QueryKey, term: string, enabled: boolean) {
  const client = useQueryClient();
  const queryKey = [...directoryKey, "search", term];
  const search = useInfiniteQuery({
    queryKey,
    initialPageParam: { path: ".", offset: 0 } as DirectorySearchCursor,
    queryFn: ({ pageParam, signal }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      return readWorkspaceSearchPage(request, term, pageParam, AbortSignal.any([signal, AbortSignal.timeout(15_000)]));
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
  useEffect(() => {
    if (!enabled || !term || !hasNextPage || isFetching || isError) return;
    // Yield between pages so results paint and input/collapse remains responsive.
    const timer = setTimeout(() => void fetchNextPage(), 25);
    return () => clearTimeout(timer);
  }, [enabled, term, hasNextPage, isFetching, isError, fetchNextPage, pageCount]);
  const key = JSON.stringify(queryKey);
  useEffect(() => {
    if (enabled) return;
    void client.cancelQueries({ queryKey: JSON.parse(key), exact: true });
  }, [client, enabled, key]);
  return { ...search, entries: search.data?.pages.flatMap((page) => page.entries) ?? [], searching: enabled && !!request && !!term && !isError && (search.isPending || isFetching || !!hasNextPage) };
}
