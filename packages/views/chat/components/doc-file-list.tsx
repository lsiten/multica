"use client";

import { ChevronRight, File, Folder, GitBranch, Loader2, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useDocModeStore, selectActiveDocument } from "@multica/core/chat";
import { useLocalDirectoryStore, selectCurrentLocalDirectory } from "@multica/core/chat/local-directory-store";
import { chatSessionFilesOptions } from "@multica/core/chat/queries";
import { api } from "@multica/core/api";
import { projectDetailOptions } from "@multica/core/projects/queries";
import { useQuery } from "@tanstack/react-query";
import { requestReviewPage } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import type { Agent, ChatSession, ChatSessionRepo, Issue } from "@multica/core/types";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { isInternalWorkspacePath } from "@multica/core/types/local-review";
import { fileIcon } from "../../editor/utils/file-icon";
import { LocalReviewIndex } from "../../issues/components/local-review-index";
import { GroupChatManagement } from "./group-chat-management";
import { Button } from "@multica/ui/components/ui/button";

const WORKSPACE_FILES_TIMEOUT_MS = 5_000;

// The files tab is the chat's server-side projection of openable source code:
// the files the session's tasks checked out of the project's workspace (task
// work_dirs). Attachments have their own tab. It reads the chat session, not
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
  projectId?: string;
  runtimeId?: string;
  workDir?: string;
  relativePath?: string;
  contentType?: string;
}

interface FileTreeNode {
  path: string;
  name: string;
  directory: boolean;
  document?: DocumentListItem;
  children: FileTreeNode[];
}

function buildFileTree(documents: DocumentListItem[]): FileTreeNode[] {
  const roots: FileTreeNode[] = [];
  for (const document of documents) {
    const parts = document.label.split("/").filter(Boolean);
    let siblings = roots;
    let path = "";
    parts.forEach((part, index) => {
      path = path ? `${path}/${part}` : part;
      let node = siblings.find((candidate) => candidate.name === part);
      if (!node) {
        node = { path, name: part, directory: index < parts.length - 1, children: [] };
        siblings.push(node);
      }
      if (index === parts.length - 1) node.document = document;
      siblings = node.children;
    });
  }
  const sort = (nodes: FileTreeNode[]): FileTreeNode[] => nodes.sort((a, b) => {
    if (a.directory !== b.directory) return a.directory ? -1 : 1;
    return a.name.localeCompare(b.name);
  }).map((node) => ({ ...node, children: sort(node.children) }));
  return sort(roots);
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

export function DocModeFileList({ wsId, sessionId, session, agents = [] }: { wsId: string; sessionId?: string | null; session?: ChatSession | null; agents?: Agent[] }) {
  const { t } = useT("chat");
  const workspaceSlug = useWorkspaceSlug();
  const isGroupChat = session?.kind === "group";
  const [activeTab, setActiveTab] = useState<"files" | "attachments" | "issues" | "group">("files");
  const [selectedProjectId, setSelectedProjectId] = useState<string | undefined>(undefined);
  const sessionFilesQuery = useQuery({
    ...chatSessionFilesOptions(wsId, sessionId ?? "", selectedProjectId, activeTab === "attachments"),
  });
  const { data: sessionFiles, isLoading: sessionFilesLoading, isError: sessionFilesError, refetch: refetchSessionFiles } = sessionFilesQuery;
  // A session's project context is its own; switching sessions must not carry the
  // previous session's selected project into the new one (which would leave the
  // panel showing a stale project title). Reset the sticky selection whenever the
  // session changes so it re-derives from the new session's files below.
  useEffect(() => {
    setSelectedProjectId(undefined);
  }, [sessionId]);
  useEffect(() => {
    if (selectedProjectId === undefined && sessionFiles) {
      setSelectedProjectId(sessionFiles.project_id ?? sessionFiles.projects?.[0]?.id);
    }
  }, [selectedProjectId, sessionFiles]);
  const projectID = selectedProjectId ?? sessionFiles?.project_id ?? "";
  const activeDoc = useDocModeStore(selectActiveDocument(sessionId ?? ""));
  const currentDirectory = useLocalDirectoryStore(selectCurrentLocalDirectory(wsId));
  const setCurrentDirectory = useLocalDirectoryStore((state) => state.setCurrentDirectory);
  const clearDirectory = useLocalDirectoryStore((state) => state.clearCurrentDirectory);
  const clearCurrentDirectory = useCallback(() => clearDirectory(wsId), [clearDirectory, wsId]);
  useEffect(() => {
    if (!isGroupChat && activeTab === "group") setActiveTab("files");
  }, [activeTab, isGroupChat]);
  useEffect(() => {
    if (!activeDoc?.workDir || !activeDoc.taskId || !activeDoc.runtimeId) return;
    setCurrentDirectory({
      path: activeDoc.workDir,
      taskId: activeDoc.taskId,
      runtimeId: activeDoc.runtimeId,
      workspaceId: wsId,
      label: activeDoc.relativePath,
    });
  }, [activeDoc?.relativePath, activeDoc?.runtimeId, activeDoc?.taskId, activeDoc?.workDir, setCurrentDirectory, wsId]);
  // The project is the header for its files; it has no document body of its
  // own, so it is shown but not opened through the doc-mode surface.
  const { data: project } = useQuery({
    ...projectDetailOptions(wsId, projectID),
    enabled: !!projectID,
  });
  const { data: projectIssues } = useQuery({
    queryKey: ["chat-project-issues", wsId, projectID],
    queryFn: () => api.listIssues({ project_id: projectID, limit: 100, offset: 0 }),
    // The file list is the default tab; do not block its first paint on the
    // issues projection. Fetch the heavier issue window only when requested.
    enabled: !!projectID && activeTab === "issues",
    staleTime: 15_000,
  });
  // Stable across renders (keyed on sessionFiles) so the derived document memo
  // below is not recomputed on every render.
  const fileTasks = useMemo(
    () => {
      const sessionTasks = (sessionFiles?.tasks ?? []).filter((task) => task.work_dir);
      const projectTasks = selectedProjectId
        ? (sessionFiles?.project_tasks ?? []).filter((task) => task.work_dir)
        : [];
      // A chat session can contain several specialist worktrees and a project
      // can contain many historical runs. The server orders the session list
      // with the lead/requirement checkout first; keep that scope intact and
      // only use project history when the chat has no checkout of its own.
      // Sessions can also contain informational tasks without a worktree; they
      // must not mask the project checkout that supplies the file tree and Git
      // scope.
      const tasks = sessionTasks.length > 0 ? sessionTasks : projectTasks;
      const ordered = sessionTasks.length > 0
        ? tasks
        : [...tasks].sort((a, b) => {
            const aRequirement = a.issue_kind === "requirement" ? 1 : 0;
            const bRequirement = b.issue_kind === "requirement" ? 1 : 0;
            if (aRequirement !== bRequirement) return bRequirement - aRequirement;
            return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
          });
      return ordered.slice(0, 25);
    },
    [selectedProjectId, sessionFiles],
  );
  // Source Control follows the directory currently open in the document
  // surface, just like VS Code. It must not be resolved through the active
  // chat's task list: switching chats does not switch the local directory, and
  // a directory opened from another chat can still be the current Git scope.
  // These task workdirs populate the project file tree. They do not select a
  // Git scope; Source Control follows the current local directory below.
  // Keep every candidate here: the first task can be a historical or stale
  // daemon binding while a later task still owns the live checkout.
  const workspaceTasks = fileTasks;
  // A single cached query replaces useQueries: useQueries returns a fresh array
  // on every render, which defeated the allDocuments memo below. Probe all
  // candidates concurrently and resolve as soon as any non-empty checkout is
  // available. A stale lead task cannot mask a later live worktree or make the
  // panel wait for its timeout; empty checkouts are retained only as a final
  // fallback after every candidate has settled.
  const workspaceFiles = useQuery({
    queryKey: ["chat-workspace-files", wsId, workspaceTasks.map((task) => task.id).join("|")],
    queryFn: async ({ signal }) => {
      const readTask = async (task: typeof workspaceTasks[number]) => {
        const requestController = new AbortController();
        let timeoutId: ReturnType<typeof setTimeout> | undefined;
        const timeout = new Promise<never>((_, reject) => {
          timeoutId = setTimeout(() => {
            requestController.abort();
            reject(new Error("workspace_files_timeout"));
          }, WORKSPACE_FILES_TIMEOUT_MS);
        });
        const abortRequest = () => requestController.abort();
        signal.addEventListener("abort", abortRequest, { once: true });
        try {
          const files = await Promise.race([
            requestReviewPage({
              task_id: task.id,
              workspace_id: wsId,
              runtime_id: task.runtime_id,
              path: task.work_dir!,
              action: "workspace_files",
              relative_path: ".",
            }, requestController.signal),
            timeout,
          ]);
          if (!("entries" in files)) throw new Error("Workspace file list response expected");
          return { task, files };
        } catch (error) {
          if (signal.aborted) throw error;
          return null;
        } finally {
          if (timeoutId !== undefined) clearTimeout(timeoutId);
          signal.removeEventListener("abort", abortRequest);
        }
      };
      const attempts = workspaceTasks.map(readTask);
      try {
        const nonEmpty = await Promise.any(attempts.map(async (attempt) => {
          const result = await attempt;
          if (!result || result.files.entries.length === 0) throw new Error("Empty workspace");
          return result;
        }));
        return [nonEmpty];
      } catch {
        const settled = await Promise.all(attempts);
        const empty = settled.find((result) => result !== null);
        return empty ? [empty] : [];
      }
    },
    enabled: !!sessionId && workspaceTasks.length > 0 && activeTab === "files",
    staleTime: 60_000,
  });
  const workspaceFileResults = workspaceFiles.data;
  const workspaceFilesFailed = workspaceTasks.length > 0 && !!workspaceFileResults && workspaceFileResults.length === 0;
  const selectedWorkspaceResult = workspaceFileResults?.[0];
  const storedDirectoryTask = currentDirectory?.taskId
    ? fileTasks.find((task) => task.id === currentDirectory.taskId)
    : undefined;
  const currentDirectoryTask = storedDirectoryTask &&
    storedDirectoryTask.work_dir === currentDirectory?.path &&
    storedDirectoryTask.runtime_id === currentDirectory?.runtimeId
    ? storedDirectoryTask
    : undefined;
  const currentDirectoryResult = currentDirectoryTask
    ? workspaceFileResults?.find((result) => result?.task.id === currentDirectoryTask.id)
    : undefined;
  // A directory selected from another chat has no task in this chat and must
  // remain usable. If it does belong to this chat, discard it after its
  // workspace_files request fails so a stale task cannot poison Git for the
  // whole panel.
  const useStoredDirectory = !!currentDirectory &&
    (!storedDirectoryTask || !!currentDirectoryTask && (workspaceFiles.isPending || !workspaceFileResults || !!currentDirectoryResult));
  const usableWorkspaceTasks = selectedWorkspaceResult ? [selectedWorkspaceResult.task] : [];
  const gitTask = useMemo(() => {
    if (useStoredDirectory) return undefined;
    const activeTask = activeDoc?.taskId
      ? usableWorkspaceTasks.find((task) => task.id === activeDoc.taskId)
      : undefined;
    return activeTask ?? usableWorkspaceTasks[0];
  }, [activeDoc?.taskId, usableWorkspaceTasks, useStoredDirectory]);
  const gitRequest: LocalReviewRequest | null = useStoredDirectory && currentDirectory?.path
    ? {
        task_id: currentDirectory.taskId,
        workspace_id: currentDirectory.workspaceId,
        runtime_id: currentDirectory.runtimeId,
        path: currentDirectory.path,
        target: currentDirectoryTask?.branch_name || "HEAD",
      }
    : gitTask?.work_dir
    ? {
        task_id: gitTask.id,
        workspace_id: wsId,
        runtime_id: gitTask.runtime_id,
        path: gitTask.work_dir,
        target: gitTask.branch_name || "HEAD",
      }
    : null;
  const openDoc = useDocModeStore((s) => s.openDoc);
  // Match the VS Code/GitHub tree convention: folders start closed so a large
  // checkout stays scannable. Search is computed from allDocuments (before the
  // tree is filtered), then matching ancestor folders are opened automatically.
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(() => new Set());
  // Local-only filter (v12: the sidebar is files + a filter box). Ephemeral UI
  // state, so it lives here, not in the persisted doc-mode store.
  const [filter, setFilter] = useState("");

  const hasOpenableWorkspaceFiles = !!selectedWorkspaceResult && selectedWorkspaceResult.files.entries.length > 0;

  // The files tab is source code only: the files the session's tasks checked out
  // of the project's workspace (task work_dirs). Attachments live in their own
  // tab ("当前附件"), so they are excluded here instead of mixing into the tree.
  const allDocuments = useMemo(() => {
    const workspaceDocuments = selectedWorkspaceResult ? (() => {
      const { task, files } = selectedWorkspaceResult;
      return files.entries
        .filter((entry) => !isInternalWorkspacePath(entry.path))
        .filter((entry) => !entry.directory)
        .map((entry): DocumentListItem => ({
          kind: "workspace_file",
          id: `${task.id}:${entry.path}`,
          label: entry.path,
          sessionId: sessionId ?? undefined,
          projectId: selectedProjectId,
          taskId: task.id,
          runtimeId: task.runtime_id,
          workDir: task.work_dir,
          relativePath: entry.path,
        }));
    })() : [];
    return workspaceDocuments;
  }, [selectedProjectId, selectedWorkspaceResult, sessionId]);

  // The attachments tab is the union of the session's and the project's
  // attachments, de-duplicated by id so an attachment shared by both is not
  // listed twice.
  const sessionAttachments = useMemo(() => {
    const seen = new Set<string>();
    const merged = [...(sessionFiles?.attachments ?? []), ...(sessionFiles?.project_attachments ?? [])];
    return merged.filter((attachment) => {
      if (seen.has(attachment.id)) return false;
      seen.add(attachment.id);
      return true;
    });
  }, [sessionFiles]);

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
  const fileTree = useMemo(() => buildFileTree(visible), [visible]);

  // Repo references answer to the same filter so a filtered view stays
  // consistent (openable rows + repo rows) and reports no-match when both
  // are excluded.
  const visibleRepoReferences = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return repoReferences;
    return repoReferences.filter((repo) => repoLabel(repo).toLowerCase().includes(query));
  }, [repoReferences, filter]);

  const hasContent = isGroupChat || !!project || !!gitRequest || allDocuments.length > 0 || repoReferences.length > 0 || sessionAttachments.length > 0 || (projectIssues?.issues.length ?? 0) > 0 || (sessionFiles?.projects?.length ?? 0) > 0;

  // A failed projection used to fall through to the empty state, which made a
  // network/daemon failure look like a chat with no files. Keep this state
  // explicit so the user can distinguish an empty project from an unavailable
  // sidebar and retry without changing chats.
  if (sessionFilesLoading && !sessionFiles) {
    return (
      <div className="flex h-full items-center justify-center p-4 text-muted-foreground" role="status">
        <div className="flex items-center gap-2">
          <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
          <p className="text-body">{t(($) => $.doc_file_list.loading)}</p>
        </div>
      </div>
    );
  }

  if (sessionFilesError && !sessionFiles) {
    return (
      <div className="flex h-full items-center justify-center p-4" role="alert">
        <div className="flex max-w-xs flex-col items-center gap-3 text-center">
          <p className="text-body text-destructive">{t(($) => $.doc_file_list.load_failed)}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => void refetchSessionFiles()}>
            {t(($) => $.doc_file_list.retry)}
          </Button>
        </div>
      </div>
    );
  }

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
  const renderTree = (nodes: FileTreeNode[], depth = 0): ReactNode[] => nodes.flatMap((node) => {
    if (node.directory) {
      const expanded = filter.trim().length > 0 || expandedDirs.has(node.path);
      return [
        <li key={`dir:${node.path}`}>
          <button
            type="button"
            className="flex w-full items-center gap-1.5 px-2 py-1.5 text-left text-body hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            style={{ paddingLeft: `${8 + depth * 14}px` }}
            aria-expanded={expanded}
            onClick={() => setExpandedDirs((current) => {
              const next = new Set(current);
              if (next.has(node.path)) next.delete(node.path); else next.add(node.path);
              return next;
            })}
          >
            <ChevronRight className={`h-3.5 w-3.5 shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} />
            <Folder className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate">{node.name}</span>
          </button>
        </li>,
        ...(expanded ? renderTree(node.children, depth + 1) : []),
      ];
    }
    const doc = node.document;
    if (!doc) return [];
    const isActive = activeDoc?.id === doc.id && activeDoc?.kind === doc.kind;
    const Icon = doc.contentType ? fileIcon(doc.contentType, doc.label) : File;
    return [
      <li key={`${doc.kind}:${doc.id}`}>
        <button
          type="button"
          onClick={() => openDoc(sessionId ?? "", { ...doc })}
          aria-current={isActive ? "true" : undefined}
          className={`flex w-full items-center gap-2 px-2 py-1.5 text-left text-body transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${isActive ? "bg-accent/10" : ""}`}
          style={{ paddingLeft: `${24 + depth * 14}px` }}
        >
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{node.name}</span>
        </button>
      </li>,
    ];
  });

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
      {(sessionFiles?.projects ?? []).length > 0 && (
        <div className="flex gap-1 overflow-x-auto border-b px-2 py-1.5" role="tablist" aria-label={t(($) => $.doc_file_list.projects_label)}>
          {(sessionFiles?.projects ?? []).map((projectTab) => (
            <button
              key={projectTab.id}
              type="button"
              role="tab"
              aria-selected={projectTab.id === projectID}
              onClick={() => setSelectedProjectId(projectTab.id)}
              className={"max-w-40 shrink-0 truncate rounded-md px-2 py-1 text-caption " + (projectTab.id === projectID ? "bg-accent font-medium" : "text-muted-foreground hover:bg-accent/50")}
            >
              {projectTab.title || projectTab.id}
            </button>
          ))}
        </div>
      )}
      <div className="flex border-b px-2 pt-2" role="tablist" aria-label={t(($) => $.doc_file_list.tabs_label)}>
        {(["files", "attachments", "issues", ...(isGroupChat ? ["group" as const] : [])] as const).map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            onClick={() => setActiveTab(tab)}
            className={
              "flex-1 border-b-2 px-2 py-1.5 text-caption transition-colors " +
              (activeTab === tab ? "border-foreground font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")
            }
          >
            {tab === "files" ? t(($) => $.doc_file_list.project_files) : tab === "attachments" ? t(($) => $.doc_file_list.attachments) : tab === "issues" ? t(($) => $.doc_file_list.issues) : t(($) => $.group.management_tab)}
          </button>
        ))}
      </div>
      {activeTab === "group" && session ? (
        <GroupChatManagement session={session} agents={agents} />
      ) : activeTab === "issues" ? (
        <div className="flex-1 overflow-y-auto">
          {projectID && (projectIssues?.issues ?? []).length > 0 ? (
            <ul className="divide-y">
              {(projectIssues?.issues ?? []).map((issue: Issue) => (
                <li key={issue.id}>
                  <AppLink href={workspaceSlug ? paths.workspace(workspaceSlug).issueDetail(issue.identifier || issue.id) : `/issues/${issue.identifier || issue.id}`} className="block px-3 py-2 hover:bg-accent/50">
                    <span className="block truncate text-caption font-medium">{issue.identifier}</span>
                    <span className="block truncate text-body">{issue.title}</span>
                  </AppLink>
                </li>
              ))}
            </ul>
          ) : (
            <p className="p-4 text-body text-muted-foreground">{t(($) => $.doc_file_list.no_issues)}</p>
          )}
        </div>
      ) : activeTab === "attachments" ? (
        <div className="flex-1 overflow-y-auto">
          {sessionAttachments.length > 0 ? (
            <ul className="divide-y">
              {sessionAttachments.map((attachment) => {
                const name = attachment.filename || attachment.url;
                const Icon = fileIcon(attachment.content_type, name);
                return (
                  <li key={attachment.id}>
                    <button type="button" onClick={() => openDoc(sessionId ?? "", { kind: "attachment", id: attachment.id, label: name, contentType: attachment.content_type, sessionId: sessionId ?? undefined })} className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/50">
                      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate text-body">{name}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="p-4 text-body text-muted-foreground">{t(($) => $.doc_file_list.no_attachments)}</p>
          )}
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          {project && (
            <div className="px-3 py-2">
              <h3 className="truncate text-body font-semibold">{project.title}</h3>
            </div>
          )}
          {activeTab === "files" && workspaceFiles.isPending && workspaceTasks.length > 0 && (
            <div className="flex items-center gap-2 px-3 py-3 text-body text-muted-foreground" role="status">
              <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
              {t(($) => $.doc_file_list.loading)}
            </div>
          )}
          {activeTab === "files" && workspaceFilesFailed && repoReferences.length === 0 && (
            <div className="flex items-center justify-between gap-3 border-b px-3 py-3" role="alert">
              <p className="text-body text-destructive">{t(($) => $.doc_file_list.load_failed)}</p>
              <Button type="button" variant="outline" size="sm" onClick={() => void workspaceFiles.refetch()}>
                {t(($) => $.doc_file_list.retry)}
              </Button>
            </div>
          )}
          {visibleRepoReferences.length > 0 && (
            <ul className="divide-y" aria-label={t(($) => $.doc_file_list.git_repositories)}>
              {visibleRepoReferences.map((repo) => (
                <li key={repo.url}>
                  <a href={repo.url} target="_blank" rel="noreferrer" className="flex w-full items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/50">
                    <GitBranch className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body font-medium">{repoLabel(repo)}</span>
                      <span className="mt-0.5 block truncate text-caption text-muted-foreground">{repo.ref ? `⎇ ${repo.ref}` : t(($) => $.doc_file_list.git_repository)}</span>
                    </span>
                  </a>
                </li>
              ))}
            </ul>
          )}
          {showNoMatch ? (
            <div className="p-4 text-muted-foreground"><p className="text-body">{t(($) => $.doc_file_list.no_match)}</p></div>
          ) : visible.length > 0 ? (
            <ul className="flex-1 overflow-y-auto" aria-label={t(($) => $.doc_file_list.project_files)}>
              {renderTree(fileTree)}
            </ul>
          ) : (
            <div className="flex-1" />
          )}
          <section className="mt-2 border-t p-2" aria-label={t(($) => $.doc_file_list.git)}>
            <div className="mb-2 flex items-center gap-2 text-caption font-medium text-muted-foreground">
              <GitBranch className="h-3.5 w-3.5" />
              {t(($) => $.doc_file_list.git)}
            </div>
            {gitRequest ? (
              <LocalReviewIndex
                request={gitRequest}
                onChanged={() => {
                  void workspaceFiles.refetch();
                }}
                onBusyChange={() => undefined}
                onInvalidBinding={clearCurrentDirectory}
              />
            ) : (
              <p className="p-2 text-body text-muted-foreground">{t(($) => $.doc_file_list.no_git_checkout)}</p>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
