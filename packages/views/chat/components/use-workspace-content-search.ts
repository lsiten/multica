import { useEffect } from "react";
import { useInfiniteQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { readWorkspaceContentSearchBatch, type ContentSearchCursor } from "./workspace-content-search";
import { WORKSPACE_DIRECTORY_READ_TIMEOUT_MS } from "./workspace-directory-query";
import { useDebouncedValue } from "../../common/use-debounced-value";

const INITIAL_CURSOR: ContentSearchCursor = { directories: { path: ".", offset: 0 }, files: [] };

export function useWorkspaceContentSearch(input: {
  readonly request: PagedReviewInput | null;
  readonly directoryKey: QueryKey;
  readonly term: string;
  readonly enabled: boolean;
}) {
  const client = useQueryClient();
  const settledTerm = useDebouncedValue(input.term, 250, input.enabled);
  const queryKey = [...input.directoryKey, "content-search", input.term];
  const key = JSON.stringify(queryKey);
  const search = useInfiniteQuery({
    queryKey,
    initialPageParam: INITIAL_CURSOR,
    queryFn: ({ pageParam, signal }) => {
      if (!input.request) throw new Error("Workspace directory is unavailable");
      return readWorkspaceContentSearchBatch({ request: input.request, term: input.term, cursor: pageParam }, AbortSignal.any([signal, AbortSignal.timeout(WORKSPACE_DIRECTORY_READ_TIMEOUT_MS)]));
    },
    getNextPageParam: (page) => page.next,
    enabled: input.enabled && !!input.request && !!input.term && input.term === settledTerm,
    structuralSharing: false,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  });
  useEffect(() => {
    if (input.enabled) return;
    void client.cancelQueries({ queryKey: JSON.parse(key), exact: true });
  }, [client, input.enabled, key]);
  const pages = search.data?.pages ?? [];
  return {
    ...search,
    matches: pages.flatMap((page) => page.matches),
    scannedFiles: pages.reduce((count, page) => count + page.scannedFiles, 0),
    failedFiles: pages.flatMap((page) => page.failedFiles),
    truncatedFiles: pages.flatMap((page) => page.truncatedFiles),
    restart: () => client.resetQueries({ queryKey, exact: true }),
  };
}
