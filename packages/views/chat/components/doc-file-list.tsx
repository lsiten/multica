"use client";

import { FileText, GitBranch, Paperclip, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useDocModeStore, selectActiveDocument } from "@multica/core/chat";
import { chatSessionFilesOptions } from "@multica/core/chat/queries";
import { projectDetailOptions } from "@multica/core/projects/queries";
import { useQuery } from "@tanstack/react-query";
import { requestReviewPage } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import type { ChatSessionRepo } from "@multica/core/types";

// The file list is the chat's server-side projection of openable documents:
// the session's attachments plus the files the session's tasks checked out of
// the project's workspace (task work_dirs). It reads the chat session, not
// client recent context, so the same list survives a refresh, another device,
// and stays isolated to the session's own workspace. The project has no
// document body of its own, so it renders as a section header rather than an
// openable row.
export interface DocumentListItem {
  kind: "attachment" | "workspace_file";
  id: string;
  label: string;
  sessionId?: string;
  taskId?: string;
  runtimeId?: string;
  relativePath?: string;
  contentType?: string;
}

// A git URL rendered as a short "owner/repo" label; falls back to the raw URL.
function repoLabel(repo: ChatSessionRepo): string {
  const name = repo.description?.trim();
  if (name) return name;
  const stripped = repo.url
    .replace(/^git@/, "")
    .replace(/^ssh:\/\//, "")
    .replace(/^[a-z][a-z+.-]*:\/\//, "")
    .replace(/\.git$/, "");
  // SSH-style "host:owner/repo" keeps the segment after the last colon.
  const path = stripped.includes(":") ? (stripped.split(":").pop() ?? stripped) : stripped;
  const parts = path.split("/").filter(Boolean);
  const short = parts.slice(-2).join("/");
  return short || repo.url;
}

export function DocModeFileList({ wsId, sessionId }: { wsId: string; sessionId?: string | null }) {
  const { t } = useT("chat");
  const { data: sessionFiles } = useQuery({
    ...chatSessionFilesOptions(wsId, sessionId ?? ""),
  });
  // The project is the header for its files; it has no document body of its
  // own, so it is shown but not opened through the doc-mode surface.
  const { data: project } = useQuery({
    ...projectDetailOptions(wsId, sessionFiles?.project_id ?? ""),
    enabled: !!sessionFiles?.project_id,
  });
  // Stable across renders (keyed on sessionFiles) so the derived document memo
  // below is not recomputed on every render.
  const fileTasks = useMemo(
    () => (sessionFiles?.tasks ?? []).filter((task) => task.work_dir).slice(0, 5),
    [sessionFiles],
  );
  // A single cached query (Promise.all) replaces useQueries: useQueries returns
  // a fresh array on every render, which defeated the allDocuments memo below.
  // A cached data array is stable across renders, so the memo holds. Per-task
  // failures are swallowed so one unreachable task neither drops every other
  // task's files nor triggers the repo-references fallback.
  const workspaceFiles = useQuery({
    queryKey: ["chat-workspace-files", wsId, fileTasks.map((task) => task.id).join("|")],
    queryFn: () =>
      Promise.all(
        fileTasks.map((task) =>
          requestReviewPage({
            task_id: task.id,
            workspace_id: wsId,
            runtime_id: task.runtime_id,
            path: task.work_dir!,
            action: "workspace_files",
            relative_path: ".",
          }).catch(() => null),
        ),
      ),
    enabled: !!sessionId && fileTasks.length > 0,
    staleTime: 15_000,
  });
  const openDoc = useDocModeStore((s) => s.openDoc);
  const activeDoc = useDocModeStore(selectActiveDocument(wsId));
  // Local-only filter (v12: the sidebar is files + a filter box). Ephemeral UI
  // state, so it lives here, not in the persisted doc-mode store.
  const [filter, setFilter] = useState("");

  const hasOpenableWorkspaceFiles = (workspaceFiles.data ?? []).some(
    (files) => files !== null && "entries" in files && files.entries.length > 0,
  );

  const allDocuments = useMemo(() => {
    const attachmentDocuments = (sessionFiles?.attachments ?? []).map(
      (attachment): DocumentListItem => ({
        kind: "attachment",
        id: attachment.id,
        label: attachment.filename || attachment.url,
        contentType: attachment.content_type,
        sessionId: sessionId ?? undefined,
      }),
    );
    const workspaceDocuments = (workspaceFiles.data ?? []).flatMap((files, taskIndex) => {
      if (!files || !("entries" in files)) return [];
      const task = fileTasks[taskIndex];
      return files.entries
        .filter((entry) => !entry.directory)
        .map((entry) => ({
          kind: "workspace_file" as const,
          id: `${task?.id ?? ""}:${entry.path}`,
          label: entry.path,
          sessionId: sessionId ?? undefined,
          taskId: task?.id,
          runtimeId: task?.runtime_id,
          relativePath: entry.path,
        }));
    });
    return [...workspaceDocuments, ...attachmentDocuments];
  }, [sessionFiles, workspaceFiles.data, fileTasks, sessionId]);

  // When the session has no task worktree to browse, the project's configured
  // repos stand in for its files. They are not openable in doc mode (there is
  // no worktree to read from), so they link out to the repository.
  const repoReferences = useMemo(() => {
    if (hasOpenableWorkspaceFiles) return [];
    return (sessionFiles?.repos ?? []).filter((repo) => repo.url);
  }, [sessionFiles, hasOpenableWorkspaceFiles]);

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return allDocuments;
    return allDocuments.filter((doc) => doc.label.toLowerCase().includes(query));
  }, [allDocuments, filter]);

  // Repo references answer to the same filter so a filtered view stays
  // consistent (openable rows + repo rows) and reports no-match when both
  // are excluded.
  const visibleRepoReferences = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return repoReferences;
    return repoReferences.filter((repo) => repoLabel(repo).toLowerCase().includes(query));
  }, [repoReferences, filter]);

  const hasContent = !!project || allDocuments.length > 0 || repoReferences.length > 0;

  if (!hasContent) {
    return (
      <div className="p-4 text-muted-foreground">
        <div className="flex items-center gap-2">
          <Search className="h-4 w-4" />
          <p className="text-body">{t(($) => $.doc_file_list.empty)}</p>
        </div>
      </div>
    );
  }

  // Only surface no-match when the filter actively excludes every openable row;
  // a bare project header (or repo references) with no files is not "no match".
  const showNoMatch = filter.trim().length > 0 && visible.length === 0 && visibleRepoReferences.length === 0;

  return (
    <div className="flex h-full flex-col">
      <div className="border-b p-2">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t(($) => $.doc_file_list.filter_placeholder)}
            aria-label={t(($) => $.doc_file_list.filter_placeholder)}
            className="w-full rounded-md border border-transparent bg-muted/50 py-1.5 pl-8 pr-2 text-body outline-none transition-colors focus:border-ring focus:bg-background"
          />
        </div>
      </div>
      {project && (
        <div className="px-3 py-2">
          <h3 className="truncate text-body font-semibold">{project.title}</h3>
        </div>
      )}
      {visibleRepoReferences.length > 0 && (
        <ul className="divide-y">
          {visibleRepoReferences.map((repo) => (
            <li key={repo.url}>
              <a
                href={repo.url}
                target="_blank"
                rel="noreferrer"
                className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/50"
              >
                <GitBranch className="h-4 w-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-body">{repoLabel(repo)}</span>
              </a>
            </li>
          ))}
        </ul>
      )}
      {showNoMatch ? (
        <div className="p-4 text-muted-foreground">
          <p className="text-body">{t(($) => $.doc_file_list.no_match)}</p>
        </div>
      ) : visible.length > 0 ? (
        <ul className="flex-1 divide-y overflow-y-auto">
          {visible.map((doc) => {
            const isActive = activeDoc?.id === doc.id && activeDoc?.kind === doc.kind;
            return (
              <li key={`${doc.kind}:${doc.id}`}>
                <button
                  type="button"
                  onClick={() => openDoc(wsId, { ...doc })}
                  aria-current={isActive ? "true" : undefined}
                  className={
                    "flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/50" +
                    (isActive ? " bg-accent/10" : "")
                  }
                >
                  {doc.kind === "workspace_file" ? (
                    <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
                  ) : (
                    <Paperclip className="h-4 w-4 shrink-0 text-muted-foreground" />
                  )}
                  <span className="min-w-0 flex-1 truncate text-body">{doc.label}</span>
                </button>
              </li>
            );
          })}
        </ul>
      ) : (
        <div className="flex-1" />
      )}
    </div>
  );
}
