import { useMemo, useState } from "react";
import { useQuery, useQueries } from "@tanstack/react-query";
import { chatFileDirectories, type ChatFileDirectory, type DocumentRef } from "@multica/core/chat";
import { useLocalDirectoryStore, selectCurrentLocalDirectory } from "@multica/core/chat/local-directory-store";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { readWorkspaceDirectory } from "./workspace-directory-query";

export function useChatFileDirectory(wsId: string, sessionId: string | null | undefined, projectId: string, files: ChatSessionFilesResponse | undefined, activeDoc: DocumentRef | null, enabled: boolean, expandedDirectories: ReadonlySet<string>) {
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
  const query = useQuery({
    queryKey: ["chat-workspace-files", wsId, sessionId, projectId, selected?.key, selected?.path, selected?.runtimeId],
    queryFn: async ({ signal }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      const timeout = AbortSignal.timeout(15_000);
      return readWorkspaceDirectory(request, ".", AbortSignal.any([signal, timeout]));
    },
    enabled: enabled && !!sessionId && !!request,
    staleTime: 60_000,
    retry: false,
  });
  const rootEntries = query.data?.entries ?? [];
  const openedPaths = query.data?.recursive === false ? [...expandedDirectories].filter((path) => {
    const ancestors = path.split("/").slice(0, -1);
    return ancestors.every((_, index) => expandedDirectories.has(ancestors.slice(0, index + 1).join("/")))
      && rootEntries.some((entry) => entry.directory && (entry.path === path || path.startsWith(`${entry.path}/`)));
  }).sort() : [];
  const children = useQueries({ queries: openedPaths.map((relativePath) => ({
    queryKey: ["chat-workspace-files", wsId, sessionId, projectId, selected?.key, selected?.path, selected?.runtimeId, relativePath],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      if (!request) throw new Error("workspace directory is unavailable");
      return readWorkspaceDirectory(request, relativePath, AbortSignal.any([signal, AbortSignal.timeout(15_000)]));
    },
    enabled: enabled && !!sessionId && !!request,
    staleTime: 60_000, retry: false,
  })) });
  const nestedQueries = new Map(openedPaths.map((path, index) => [path, children[index]]));
  const entries = [...(query.data?.entries ?? []), ...children.flatMap((child) => child.data?.entries ?? [])];
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
  return { directories, selected, select, query, gitRequest, entries, nestedQueries };
}
