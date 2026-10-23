import { useMemo, useState } from "react";
import { useInfiniteQuery, useQueries, useQueryClient } from "@tanstack/react-query";
import { chatFileDirectories, type ChatFileDirectory, type DocumentRef } from "@multica/core/chat";
import { useLocalDirectoryStore, selectCurrentLocalDirectory } from "@multica/core/chat/local-directory-store";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { useWorkspaceFileSearch } from "./use-workspace-file-search";
import { readWorkspaceDirectory, WORKSPACE_DIRECTORY_READ_TIMEOUT_MS } from "./workspace-directory-query";

export function useChatFileDirectory(wsId: string, sessionId: string | null | undefined, projectId: string, files: ChatSessionFilesResponse | undefined, activeDoc: DocumentRef | null, enabled: boolean, expandedDirectories: ReadonlySet<string>, searchTerm: string) {
  const client = useQueryClient();
  const current = useLocalDirectoryStore(selectCurrentLocalDirectory(wsId));
  const setCurrentDirectory = useLocalDirectoryStore((state) => state.setCurrentDirectory);
  const directories = useMemo(() => chatFileDirectories(files, projectId), [files, projectId]);
  const [selection, setSelection] = useState<Record<string, string>>({});
  const scope = JSON.stringify([wsId, sessionId, projectId]);
  const matchesDocument = (directory: ChatFileDirectory) =>
    !!activeDoc && directory.taskId === activeDoc.taskId && directory.projectResourceId === activeDoc.projectResourceId && (!activeDoc.workDir || directory.path === activeDoc.workDir);
  const matchesCurrent = (directory: ChatFileDirectory) =>
    !!current && directory.path === current.path && directory.runtimeId === current.runtimeId && directory.taskId === current.taskId && directory.projectResourceId === current.projectResourceId;
  const selected = directories.find((directory) => directory.key === selection[scope])
    ?? directories.find(matchesDocument)
    ?? directories.find(matchesCurrent)
    ?? directories[0];
  const request: PagedReviewInput | null = selected?.runtimeId ? {
    task_id: selected.taskId ?? "",
    project_resource_id: selected.projectResourceId,
    workspace_id: wsId,
    runtime_id: selected.runtimeId,
    path: selected.path,
    target: selected.branchName || "HEAD",
  } : null;
  const directoryKey = ["chat-workspace-files", wsId, sessionId, projectId, selected?.key, selected?.path, selected?.runtimeId];
  const browse = useInfiniteQuery({
    queryKey: directoryKey,
    initialPageParam: 0,
    getNextPageParam: (page: Awaited<ReturnType<typeof readWorkspaceDirectory>>) => page.has_more ? page.next_offset : undefined,
    queryFn: async ({ signal, pageParam }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      const timeout = AbortSignal.timeout(WORKSPACE_DIRECTORY_READ_TIMEOUT_MS);
      return readWorkspaceDirectory(request, ".", AbortSignal.any([signal, timeout]), pageParam);
    },
    enabled: enabled && !searchTerm && !!sessionId && !!request,
    staleTime: 60_000,
    retry: false,
  });
  const query = { ...browse, data: browse.data?.pages[0] };
  const rootEntries = browse.data?.pages.flatMap((page) => page.entries) ?? [];
  const [paging, setPaging] = useState<{ scope: string; offsets: Record<string, number[]> }>({ scope: "", offsets: {} });
  const pagingScope = JSON.stringify(directoryKey);
  const offsets = paging.scope === pagingScope ? paging.offsets : {};
  const openedPaths = query.data?.recursive === false ? [...expandedDirectories].filter((path) => {
    const ancestors = path.split("/").slice(0, -1);
    return ancestors.every((_, index) => expandedDirectories.has(ancestors.slice(0, index + 1).join("/")))
      && rootEntries.some((entry) => entry.directory && (entry.path === path || path.startsWith(`${entry.path}/`)));
  }).sort() : [];
  const pageRequests = openedPaths.flatMap((path) => [0, ...(offsets[path] ?? [])].map((offset) => ({path, offset})));
  const children = useQueries({ queries: pageRequests.map(({path, offset}) => ({
    queryKey: [...directoryKey, path, offset],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      return readWorkspaceDirectory(request, path, AbortSignal.any([signal, AbortSignal.timeout(WORKSPACE_DIRECTORY_READ_TIMEOUT_MS)]), offset);
    },
    enabled: enabled && !searchTerm && !!sessionId && !!request,
    staleTime: 60_000, retry: false,
  })) });
  const nestedQueries = new Map(openedPaths.map((path) => {
    const pages = children.filter((_, index) => pageRequests[index]?.path === path);
    const last = pages.at(-1);
    return [path, {
      isPending: pages.some((page) => page.isPending),
      isFetching: pages.some((page) => page.isFetching),
      isError: pages.some((page) => page.isError),
      hasMore: !!last?.data?.has_more,
      refetch: () => Promise.all(pages.filter((page) => page.isError).map((page) => page.refetch())),
      loadMore: () => {
        const offset = last?.data?.next_offset;
        if (!last?.data?.has_more || offset === undefined) return;
        setPaging((previous) => {
          const current = previous.scope === pagingScope ? previous.offsets : {};
          return { scope: pagingScope, offsets: { ...current, [path]: [...new Set([...(current[path] ?? []), offset])] } };
        });
      },
    }] as const;
  }));
  const search = useWorkspaceFileSearch(request, directoryKey, searchTerm, enabled && !!sessionId);
  const entries = searchTerm ? search.entries : [...rootEntries, ...children.flatMap((child) => child.data?.entries ?? [])];
  const select = (key: string | null) => {
    const directory = directories.find((candidate) => candidate.key === key);
    if (!directory) return;
    setSelection((previous) => ({ ...previous, [scope]: directory.key }));
    if (directory.runtimeId) setCurrentDirectory({
      path: directory.path, taskId: directory.taskId, projectResourceId: directory.projectResourceId,
      runtimeId: directory.runtimeId, workspaceId: wsId, label: directory.label,
    });
  };
  // Explicit directory selection and opened files define Git's scope. Keep the
  // existing global directory usable when this chat has no directory of its own.
  const gitRequest: PagedReviewInput | null = request && (selection[scope] || selected?.projectResourceId || (selected && (matchesDocument(selected) || matchesCurrent(selected))))
    ? request
    : !selected && current && (current.taskId || current.projectResourceId)
    ? { task_id: current.taskId ?? "", project_resource_id: current.projectResourceId, workspace_id: wsId, runtime_id: current.runtimeId, path: current.path, target: "HEAD" }
    : null;
  return { directories, selected, select, query, gitRequest, entries, nestedQueries, search, hasMore: browse.hasNextPage, loadMore: () => browse.fetchNextPage(), refresh: () => Promise.all([
    client.resetQueries({ queryKey: [...directoryKey, "search"] }),
    client.invalidateQueries({ queryKey: directoryKey, predicate: (query) => query.queryKey[directoryKey.length] !== "search" }),
  ]) };
}
