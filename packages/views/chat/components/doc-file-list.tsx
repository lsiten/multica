"use client";
import { issueRouteRef } from "@multica/core/paths/paths";

import { ChevronRight, Eye, File, Folder, FolderOpen, GitBranch, Loader2, Search } from "@multica/ui/icons";
import { useCallback, useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { documentDraftKey, useDocModeStore, selectActiveDocument } from "@multica/core/chat";
import { useLocalDirectoryStore } from "@multica/core/chat/local-directory-store";
import { chatSessionFilesOptions } from "@multica/core/chat/queries";
import { api, ApiError } from "@multica/core/api";
import { projectDetailOptions, projectKeys } from "@multica/core/projects/queries";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import type { Agent, ChatSession, ChatSessionRepo, Issue } from "@multica/core/types";
import { isInternalWorkspacePath } from "@multica/core/types/local-review";
import { fileIcon } from "../../editor/utils/file-icon";
import { ChatChangesPanel } from "./chat-changes-panel";
import { ChatProjectSelector } from "./chat-project-selector";
import { GroupChatManagement } from "./group-chat-management";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { useChatFileDirectory } from "./use-chat-file-directory";
import { ChatDirectorySelector } from "./chat-directory-selector";
import { WorkspaceFileSearchStatus } from "./workspace-file-search-status";
import { useWorkspaceContentSearch } from "./use-workspace-content-search";
import { WorkspaceContentSearchResults, WorkspaceContentSearchStatus } from "./workspace-content-search-results";
import { WorkspaceFileCreate } from "./workspace-file-create";
import { WorkspaceFileActions } from "./workspace-file-actions";
import { WorkspaceFilePending } from "./workspace-file-pending";
import { WorkspaceFileTrash } from "./workspace-file-trash";
import { WorkspaceFileOperationDialog, type WorkspaceFileChoice } from "./workspace-file-operation-dialog";
import { chatKeys } from "@multica/core/chat/queries";

// Files come from the selected project's bound directories and run worktrees.
// The server projects their identities; the owning runtime reads the chosen
// directory. Attachments remain separate from these filesystem entries.
export interface DocumentListItem {
  kind: "attachment" | "workspace_file";
  id: string;
  label: string;
  sessionId?: string;
  taskId?: string;
  projectResourceId?: string;
  projectId?: string;
  runtimeId?: string;
  workDir?: string;
  relativePath?: string;
  readonly fileId?: string;
  readonly directoryId?: string;
  contentType?: string;
  directory?: boolean;
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
        node = { path, name: part, directory: index < parts.length - 1 || !!document.directory, children: [] };
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

function normalizeWorkspacePath(path: string): string {
  return path.replaceAll("\\", "/").replace(/^\.\/+/, "").replace(/^\/+|\/+$/g, "");
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

type FileListProps = { wsId: string; sessionId?: string | null; session?: ChatSession | null; agents?: Agent[] };
type AttachmentScope = "outputs" | "references" | "all";

export function DocModeFileList(props: FileListProps) {
  const [choice, setChoice] = useState<WorkspaceFileChoice | null>(null);
  return <div className="flex h-full min-h-0 min-w-0 flex-col">
    <WorkspaceFilePending wsId={props.wsId} onResume={(pending) => setChoice({ pending })} />
    <DocModeFileListContent {...props} onChoose={setChoice} onProjectSwitch={() => setChoice(null)} />
    {choice && <WorkspaceFileOperationDialog key={"pending" in choice ? choice.pending.commandId : JSON.stringify([choice.context, choice.operation])} choice={choice} onClose={() => setChoice(null)} />}
  </div>;
}

function DocModeFileListContent({ wsId, sessionId, session, agents = [], onChoose, onProjectSwitch }: FileListProps & { readonly onChoose: (choice: WorkspaceFileChoice) => void; readonly onProjectSwitch: () => void }) {
  const { t } = useT("chat");
  const workspaceSlug = useWorkspaceSlug();
  const isGroupChat = session?.kind === "group";
  const [activeTab, setActiveTab] = useState<"files" | "changes" | "attachments" | "issues" | "group">(isGroupChat ? "group" : "files");
  const [attachmentScope, setAttachmentScope] = useState<AttachmentScope>("all");
  const [selectedProjectId, setSelectedProjectId] = useState<string | undefined>(undefined);
  const queryClient = useQueryClient();
  const selectedProjectMutation = useMutation({
    mutationFn: (data: { sessionId: string; projectId: string | null }) => api.updateChatSession(data.sessionId, { selected_project_id: data.projectId }),
    onMutate: () => ({ previousProjectId: selectedProjectId }),
    onError: (error, _variables, context) => {
      // A deleted/revoked project must not leave the drawer pinned to an
      // invalid query key. Clear the selection so the server can re-derive
      // the valid project set and the UI asks the user again.
      if (error instanceof ApiError && (error.status === 403 || error.status === 404)) {
        setSelectedProjectId(undefined);
        useLocalDirectoryStore.getState().clearCurrentDirectory(wsId);
        if (sessionId) useDocModeStore.getState().selectChat(sessionId);
        return;
      }
      if (context) setSelectedProjectId(context.previousProjectId);
    },
    onSettled: (_data, _error, variables) => {
      void queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      void queryClient.invalidateQueries({ queryKey: [...chatKeys.all(wsId), "session-files", variables.sessionId] });
    },
  });
  const sessionFilesQuery = useQuery({
    ...chatSessionFilesOptions(wsId, sessionId ?? "", selectedProjectId, activeTab === "attachments"),
  });
  const { data: sessionFiles, isLoading: sessionFilesLoading, isError: sessionFilesError, error: sessionFilesQueryError, refetch: refetchSessionFiles } = sessionFilesQuery;
  // A session's project context is its own; switching sessions must not carry the
  // previous session's selected project into the new one (which would leave the
  // panel showing a stale project title). Reset the sticky selection whenever the
  // session changes so it re-derives from the new session's files below.
  useEffect(() => {
    setSelectedProjectId(undefined);
  }, [sessionId]);
  useEffect(() => {
    if (selectedProjectId === undefined && sessionFiles) {
      setSelectedProjectId(
        sessionFiles.selected_project_id ??
          (sessionFiles.selection_required ? undefined : sessionFiles.project_id ?? undefined),
      );
    }
  }, [selectedProjectId, sessionFiles]);
  useEffect(() => {
    if (!selectedProjectId || !(sessionFilesQueryError instanceof ApiError) || ![403, 404].includes(sessionFilesQueryError.status)) return;
    setSelectedProjectId(undefined);
    useLocalDirectoryStore.getState().clearCurrentDirectory(wsId);
    if (sessionId) useDocModeStore.getState().selectChat(sessionId);
  }, [selectedProjectId, sessionFilesQueryError, sessionId, wsId]);
  const projectID = selectedProjectId ??
    (sessionFiles?.selection_required ? "" : sessionFiles?.project_id ?? "");
  const activeDoc = useDocModeStore(selectActiveDocument(sessionId ?? ""));
  const setCurrentDirectory = useLocalDirectoryStore((state) => state.setCurrentDirectory);
  const clearDirectory = useLocalDirectoryStore((state) => state.clearCurrentDirectory);
  const clearCurrentDirectory = useCallback(() => clearDirectory(wsId), [clearDirectory, wsId]);
  useEffect(() => {
    if (!isGroupChat && activeTab === "group") setActiveTab("files");
  }, [activeTab, isGroupChat]);
  // A group chat opens on its management tab; a normal chat on its files.
  // Reset the sticky selection whenever the session changes so a group chat
  // never inherits a previous session's tab (the panel may stay mounted
  // across sessions while it is open).
  useEffect(() => {
    setActiveTab(isGroupChat ? "group" : "files");
    setAttachmentScope("all");
  }, [sessionId, isGroupChat]);
  useEffect(() => {
    if (!activeDoc?.workDir || (!activeDoc.taskId && !activeDoc.projectResourceId) || !activeDoc.runtimeId) return;
    setCurrentDirectory({
      path: activeDoc.workDir,
      taskId: activeDoc.taskId,
      projectResourceId: activeDoc.projectResourceId,
      runtimeId: activeDoc.runtimeId,
      workspaceId: wsId,
      label: activeDoc.relativePath,
    });
  }, [activeDoc?.projectResourceId, activeDoc?.relativePath, activeDoc?.runtimeId, activeDoc?.taskId, activeDoc?.workDir, setCurrentDirectory, wsId]);
  // The project is the header for its files; it has no document body of its
  // own, so it is shown but not opened through the doc-mode surface.
  const { data: project } = useQuery({
    ...projectDetailOptions(wsId, projectID),
    enabled: !!projectID,
  });
  const projectIssuesQuery = useQuery({
    queryKey: [...projectKeys.all(wsId), "chat-open-issues", projectID],
    queryFn: () => api.listIssues({ project_id: projectID, open_only: true }),
    // The file list is the default tab; do not block its first paint on the
    // issues projection. Fetch the heavier issue window only when requested.
    enabled: !!projectID && activeTab === "issues",
    staleTime: 15_000,
  });
  const projectIssues = projectIssuesQuery.data;
  const [filter, setFilter] = useState("");
  const [searchMode, setSearchMode] = useState<"name" | "content">("name");
  const searchModeId = useId();
  const searchTerm = filter.trim().toLowerCase();
  const [searchCollapsed, setSearchCollapsed] = useState<{scope: string; paths: Set<string>}>({scope:"", paths:new Set()});
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(() => new Set());
  const { entries: workspaceEntries, nestedQueries, directories, selected: selectedDirectory, select: selectDirectory, query: workspaceFiles, request, directoryKey, gitRequest, search, hasMore, loadMore, refresh: refreshDirectory } = useChatFileDirectory(
    wsId, sessionId, projectID, sessionFiles, activeDoc, activeTab === "files", expandedDirs, { term: searchTerm, mode: searchMode },
  );
  const selectProject = useCallback((projectId: string | null) => {
    onProjectSwitch();
    setSelectedProjectId(projectId ?? undefined);
    setActiveTab(isGroupChat ? "group" : "files");
    setFilter("");
    setSearchCollapsed({ scope: "", paths: new Set() });
    setExpandedDirs(new Set());
    clearCurrentDirectory();
    if (sessionId) useDocModeStore.getState().selectChat(sessionId);
    if (sessionId) selectedProjectMutation.mutate({ sessionId, projectId });
  }, [clearCurrentDirectory, isGroupChat, onProjectSwitch, selectedProjectMutation, sessionId]);
  const contentSearch = useWorkspaceContentSearch({ request, directoryKey, term: filter.trim(), enabled: activeTab === "files" && searchMode === "content" && !!sessionId });
  const openDoc = useDocModeStore((s) => s.openDoc);
  const searchScope = JSON.stringify([wsId, sessionId, projectID, selectedDirectory?.key, searchTerm]);
  const collapsedPaths = searchCollapsed.scope === searchScope ? searchCollapsed.paths : new Set<string>();
  const resourceDirectoryId = workspaceFiles.data?.directory_id ?? search.data?.pages.find((page) => page.directoryId)?.directoryId;
  const operationContext = selectedDirectory?.taskId && selectedDirectory.runtimeId && !selectedDirectory.projectResourceId
    ? { workspace_id: wsId, task_id: selectedDirectory.taskId, runtime_id: selectedDirectory.runtimeId, path: selectedDirectory.path } : null;
  const operationsSupported = workspaceFiles.data?.file_operations_supported === true && workspaceFiles.data.file_operation_identity_supported === true;
  const fileActions = (doc: DocumentListItem | undefined) => operationContext && doc?.fileId && doc.relativePath
    ? <WorkspaceFileActions context={operationContext} path={doc.relativePath} fileId={doc.fileId} directory={!!doc.directory} supported={operationsSupported} onChoose={onChoose} /> : null;

  const allDocuments = useMemo((): DocumentListItem[] => {
    if (!selectedDirectory) return [];
    return workspaceEntries
      .filter((entry) => !isInternalWorkspacePath(entry.path))
      .map((entry) => {
        const relativePath = normalizeWorkspacePath(entry.path);
        return {
          kind: "workspace_file",
          directory: entry.directory,
          id: `${selectedDirectory.taskId ?? selectedDirectory.key}:${relativePath}`,
          label: relativePath,
          sessionId: sessionId ?? undefined,
          projectId: projectID || undefined,
          taskId: selectedDirectory.taskId,
          projectResourceId: selectedDirectory.projectResourceId,
          runtimeId: selectedDirectory.runtimeId,
          workDir: selectedDirectory.path,
          relativePath,
          fileId: entry.file_id,
          directoryId: resourceDirectoryId,
        };
      });
  }, [projectID, selectedDirectory, sessionId, workspaceEntries, resourceDirectoryId]);

  // The attachments tab is the union of the session's and the project's
  // attachments, de-duplicated by id so an attachment shared by both is not
  // listed twice.
  const attachmentRows = useMemo(() => {
    const seen = new Set<string>();
    const rows = [
      ...(sessionFiles?.attachments ?? []).map((attachment) => ({ attachment, source: "output" as const })),
      ...(sessionFiles?.project_attachments ?? []).map((attachment) => ({ attachment, source: "reference" as const })),
    ];
    return rows.filter(({ attachment }) => {
      if (seen.has(attachment.id)) return false;
      seen.add(attachment.id);
      return true;
    });
  }, [sessionFiles]);
  const visibleAttachmentRows = useMemo(
    () => attachmentRows.filter(({ source }) => attachmentScope === "all" || source === attachmentScope.slice(0, -1)),
    [attachmentRows, attachmentScope],
  );

  // Repository links are references only when no filesystem roots are bound.
  // They must never hide an unavailable or empty selected directory.
  const repoReferences = useMemo(() => {
    if (directories.length > 0) return [];
    return (sessionFiles?.repos ?? []).filter((repo) => repo.url);
  }, [sessionFiles, directories.length]);

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

  const hasContent = isGroupChat || !!project || directories.length > 0 || !!gitRequest || allDocuments.length > 0 || repoReferences.length > 0 || attachmentRows.length > 0 || (projectIssues?.issues.length ?? 0) > 0 || (sessionFiles?.projects?.length ?? 0) > 0;

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
  const showNoMatch = !!searchTerm && !search.searching && !search.hasNextPage && !search.isError && visible.length === 0 && visibleRepoReferences.length === 0;
  const displayedDirectory = selectedDirectory?.path;
  const renderTree = (nodes: FileTreeNode[], depth = 0): ReactNode[] => nodes.flatMap((node) => {
    if (node.directory) {
      const expanded = searchTerm ? !collapsedPaths.has(node.path) && node.children.length > 0 : expandedDirs.has(node.path);
      return [
        <li key={`dir:${node.path}`} className="flex min-w-0 items-center pr-1">
          <button
            type="button"
            className="flex min-w-0 flex-1 items-center gap-1.5 px-2 py-1.5 text-left text-body hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            style={{ paddingLeft: `${8 + depth * 14}px` }}
            aria-expanded={expanded}
            onClick={() => {
              if (searchTerm) {
                setSearchCollapsed((current) => {
                  const next = new Set(current.scope === searchScope ? current.paths : []);
                  if (expanded) next.add(node.path); else next.delete(node.path);
                  return {scope:searchScope, paths:next};
                });
                return;
              }
              setExpandedDirs((current) => {
                const next = new Set(current);
                if (next.has(node.path)) next.delete(node.path); else next.add(node.path);
                return next;
              });
            }}
          >
            <ChevronRight className={`h-3.5 w-3.5 shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} />
            <Folder className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate">{node.name}</span>
          </button>
          {fileActions(node.document)}
        </li>,
        ...(expanded && !searchTerm && nestedQueries.get(node.path)?.isPending ? [<li key={`loading:${node.path}`} role="status" className="px-3 py-1.5 text-body text-muted-foreground">{t(($) => $.doc_file_list.loading)}</li>] : []),
        ...(expanded && !searchTerm && nestedQueries.get(node.path)?.isError ? [<li key={`error:${node.path}`} role="alert" className="px-3 py-1.5 text-body text-destructive">{t(($) => $.doc_file_list.load_failed)} <Button type="button" size="sm" variant="ghost" onClick={() => void nestedQueries.get(node.path)?.refetch()}>{t(($) => $.doc_file_list.retry)}</Button></li>] : []),
        ...(expanded ? renderTree(node.children, depth + 1) : []),
        ...(expanded && !searchTerm && nestedQueries.get(node.path)?.hasMore ? [<li key={`more:${node.path}`} style={{paddingLeft:`${22 + depth * 14}px`}}><Button type="button" variant="ghost" size="sm" disabled={nestedQueries.get(node.path)?.isFetching} onClick={() => nestedQueries.get(node.path)?.loadMore()}>{t(($) => $.doc_file_list.load_more)}</Button></li>] : []),
      ];
    }
    const doc = node.document;
    if (!doc) return [];
    const isActive = !!activeDoc && documentDraftKey(activeDoc) === documentDraftKey(doc);
    const Icon = doc.contentType ? fileIcon(doc.contentType, doc.label) : File;
    return [
      <li key={`${doc.kind}:${doc.id}`} className="flex min-w-0 items-center pr-1">
        <button
          type="button"
          onClick={() => openDoc(sessionId ?? "", { ...doc })}
          aria-current={isActive ? "true" : undefined}
          className={`flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left text-body transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${isActive ? "bg-accent/10" : ""}`}
          style={{ paddingLeft: `${24 + depth * 14}px` }}
        >
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{node.name}</span>
        </button>
        {fileActions(doc)}
      </li>,
    ];
  });

  const fileTabs: Array<"files" | "changes" | "attachments" | "issues" | "group"> = ["files"];
  // Source Control is a first-class workspace tab even when the selected
  // project/directory has no Git binding. The empty state belongs inside the
  // tab; keeping it inline under Project files makes the desktop drawer look
  // like Git is part of the file tree and prevents a stable tab target.
  fileTabs.push("changes");
  fileTabs.push("attachments", "issues");
  if (isGroupChat) fileTabs.push("group");

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
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
        {activeTab === "files" && selectedDirectory && <div className="flex items-center gap-2 pt-2 text-caption text-muted-foreground">
          <label htmlFor={searchModeId}>{t(($) => $.doc_file_list.search_mode)}</label>
          <select id={searchModeId} className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={searchMode} onChange={(event) => setSearchMode(event.target.value === "content" ? "content" : "name")}>
            <option value="name">{t(($) => $.doc_file_list.name_search)}</option>
            <option value="content">{t(($) => $.doc_file_list.content_search)}</option>
          </select>
        </div>}
        {activeTab === "files" && searchTerm && searchMode === "name" && <WorkspaceFileSearchStatus search={search} />}
        {activeTab === "files" && searchTerm && searchMode === "content" && !!request && <WorkspaceContentSearchStatus search={contentSearch} />}
      </div>
      {(sessionFiles?.projects ?? []).length > 0 && (
        <div className="border-b p-2">
          <ChatProjectSelector
            projects={sessionFiles?.projects ?? []}
            selectedProjectId={projectID || null}
            selectionRequired={sessionFiles?.selection_required === true && !projectID}
            onSelect={selectProject}
          />
        </div>
      )}
      <div className="flex border-b px-2 pt-2" role="tablist" aria-label={t(($) => $.doc_file_list.tabs_label)}>
        {fileTabs.map((tab) => (
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
            {tab === "files" ? t(($) => $.doc_file_list.project_files) : tab === "changes" ? t(($) => $.doc_file_list.git) : tab === "attachments" ? t(($) => $.doc_file_list.attachments) : tab === "issues" ? t(($) => $.doc_file_list.issues) : t(($) => $.group.management_tab)}
          </button>
        ))}
      </div>
      {activeTab === "group" && session ? (
        <GroupChatManagement session={session} agents={agents} />
      ) : activeTab === "issues" ? (
        <div className="flex-1 overflow-y-auto">
          {projectID && projectIssuesQuery.isPending ? <div role="status" className="flex justify-center p-4"><Loader2 className="size-4 animate-spin" aria-label={t(($) => $.list.loading)} /></div>
            : projectID && projectIssuesQuery.isError ? <div className="space-y-2 p-4"><p role="alert" className="text-body text-destructive">{t(($) => $.doc_file_list.load_failed)}</p><Button variant="outline" size="sm" onClick={() => void projectIssuesQuery.refetch()}>{t(($) => $.doc_mode.retry)}</Button></div>
            : projectID && (projectIssues?.issues ?? []).length > 0 ? (
            <ul className="divide-y">
              {(projectIssues?.issues ?? []).map((issue: Issue) => (
                <li key={issue.id} className="flex items-center pr-2">
                  <AppLink href={workspaceSlug ? paths.workspace(workspaceSlug).issueDetail(issueRouteRef(issue)) : `/issues/${issue.identifier || issue.id}`} className="min-w-0 flex-1 px-3 py-2 hover:bg-accent/50">
                    <span className="block truncate text-caption font-medium">{issue.identifier}</span>
                    <span className="block truncate text-body">{issue.title}</span>
                  </AppLink>
                  <IconButton
                    label={`${t(($) => $.doc_mode.preview)} ${issue.title}`}
                    className="shrink-0"
                    disabled={!sessionId}
                    onClick={() => openDoc(sessionId ?? "", {
                      kind: "issue",
                      id: issue.id,
                      label: issue.title,
                      contentType: "text/markdown",
                      sessionId: sessionId ?? undefined,
                      projectId: projectID,
                    })}
                  >
                    <Eye />
                  </IconButton>
                </li>
              ))}
            </ul>
          ) : (
            <p className="p-4 text-body text-muted-foreground">{t(($) => $.doc_file_list.no_issues)}</p>
          )}
        </div>
      ) : activeTab === "changes" ? (
        <div className="flex-1 overflow-y-auto p-2">
          {gitRequest ? (
            <>
              <div className="mb-2 flex items-center justify-between gap-2 rounded-md border bg-muted/30 px-2 py-1.5 text-caption">
                <span className="font-medium">{t(($) => $.doc_file_list.git)}</span>
                <span className="text-muted-foreground">
                  {gitRequest.task_id ? t(($) => $.doc_file_list.changes_scope_run) : t(($) => $.doc_file_list.changes_scope_directory)}
                </span>
              </div>
              <ChatChangesPanel
                key={`${gitRequest.task_id || gitRequest.project_resource_id}:${gitRequest.path}:changes`}
                request={gitRequest}
                onChanged={() => { void refreshDirectory(); }}
                onInvalidBinding={clearCurrentDirectory}
              />
            </>
          ) : (
            <p className="p-2 text-body text-muted-foreground">{t(($) => $.doc_file_list.no_git_checkout)}</p>
          )}
        </div>
      ) : activeTab === "attachments" ? (
        <div className="flex-1 overflow-y-auto">
          <div className="flex gap-1 border-b px-2 py-1.5" role="tablist" aria-label={t(($) => $.doc_file_list.attachments)}>
            {(["outputs", "references", "all"] as const).map((scope) => (
              <button
                key={scope}
                type="button"
                role="tab"
                aria-selected={attachmentScope === scope}
                onClick={() => setAttachmentScope(scope)}
                className={"flex-1 rounded-md px-2 py-1 text-caption transition-colors " + (attachmentScope === scope ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:text-foreground")}
              >
                {scope === "outputs" ? t(($) => $.doc_file_list.attachment_outputs) : scope === "references" ? t(($) => $.doc_file_list.attachment_references) : t(($) => $.doc_file_list.attachment_all)}
              </button>
            ))}
          </div>
          {visibleAttachmentRows.length > 0 ? (
            <ul className="divide-y">
              {visibleAttachmentRows.map(({ attachment, source }) => {
                const name = attachment.filename || attachment.url;
                const Icon = fileIcon(attachment.content_type, name);
                return (
                  <li key={attachment.id}>
                    <button type="button" onClick={() => openDoc(sessionId ?? "", { kind: "attachment", id: attachment.id, label: name, contentType: attachment.content_type, sessionId: sessionId ?? undefined })} className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/50">
                      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate text-body">{name}</span>
                      <span className="shrink-0 text-caption text-muted-foreground">
                        {attachment.task_id
                          ? t(($) => $.doc_file_list.attachment_source_run, { id: attachment.task_id.slice(0, 8) })
                          : source === "output"
                            ? t(($) => $.doc_file_list.attachment_source_output)
                            : t(($) => $.doc_file_list.attachment_source_reference)}
                      </span>
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
          {sessionFiles?.selection_required && !projectID && (
            <p className="m-3 rounded-md border border-dashed p-3 text-body text-muted-foreground">{t(($) => $.doc_file_list.select_project_hint)}</p>
          )}
          {project && (
            <div className="px-3 py-2">
              <h3 className="truncate text-body font-semibold">{project.title}</h3>
            </div>
          )}
          {selectedDirectory && (
            <div className="space-y-2 px-3 pb-2">
              {directories.length > 1 || selectedDirectory.scope === "project_task" ? (
                <ChatDirectorySelector key={JSON.stringify([wsId, sessionId, selectedProjectId])} directories={directories} selected={selectedDirectory} onSelect={selectDirectory} />
              ) : (
                <div className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground" title={displayedDirectory}>
                  <FolderOpen className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                  <span className="shrink-0">{t(($) => $.doc_file_list.current_directory)}</span>
                  <span className="min-w-0 truncate font-mono" dir="ltr">{displayedDirectory}</span>
                </div>
              )}
              {selectedDirectory.projectResourceId && <p className="text-caption text-muted-foreground">{t(($) => $.doc_file_list.read_only)}</p>}
              {operationContext && <div className="flex flex-wrap items-center gap-1"><WorkspaceFileCreate
                key={JSON.stringify([wsId, selectedDirectory.runtimeId, selectedDirectory.path])}
                supported={workspaceFiles.data?.file_operations_supported === true && workspaceFiles.data.file_operation_identity_supported === true}
                context={operationContext}
                onCreated={(result) => {
                  void refreshDirectory();
                  if (!result.directory) openDoc(sessionId ?? "", {
                    kind: "workspace_file", id: `${selectedDirectory.taskId}:${result.path}`, label: result.path,
                    sessionId: sessionId ?? undefined, projectId: projectID || undefined,
                    taskId: selectedDirectory.taskId, runtimeId: selectedDirectory.runtimeId,
                    workDir: selectedDirectory.path, relativePath: result.path,
                    fileId: result.file_id, directoryId: result.directory_id, directoryRevision: result.directory_revision,
                  });
                }}
              /><WorkspaceFileTrash context={operationContext} supported={operationsSupported} onChoose={onChoose} /></div>}
            </div>
          )}
          {activeTab === "files" && !searchTerm && workspaceFiles.isPending && selectedDirectory?.runtimeId && (
            <div className="flex items-center gap-2 px-3 py-3 text-body text-muted-foreground" role="status">
              <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
              {t(($) => $.doc_file_list.loading)}
            </div>
          )}
          {activeTab === "files" && selectedDirectory && ((!searchTerm && workspaceFiles.isError) || !selectedDirectory.runtimeId) && (
            <div className="space-y-2 px-3 py-3" role="alert">
              <p className="text-body text-destructive">{t(($) => !selectedDirectory.runtimeId ? $.doc_file_list.directory_unavailable : $.doc_file_list.load_failed)}</p>
              {workspaceFiles.error instanceof Error && (
                <details className="text-caption text-muted-foreground">
                  <summary className="cursor-pointer">{t(($) => $.doc_file_list.error_details)}</summary>
                  <p className="mt-1 break-words">{workspaceFiles.error.message}</p>
                </details>
              )}
              <Button type="button" variant="outline" size="sm" onClick={() => {
                void refetchSessionFiles();
                if (selectedDirectory.runtimeId) void (workspaceFiles.isFetchNextPageError ? loadMore() : workspaceFiles.refetch());
              }}>
                {t(($) => $.doc_file_list.retry)}
              </Button>
            </div>
          )}
          {activeTab === "files" && !searchTerm && selectedDirectory && workspaceFiles.isSuccess && allDocuments.length === 0 && (
            <p className="px-3 py-3 text-body text-muted-foreground">{t(($) => $.doc_file_list.directory_empty)}</p>
          )}
          {!(searchTerm && searchMode === "content") && visibleRepoReferences.length > 0 && (
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
          {searchTerm && searchMode === "content" ? <WorkspaceContentSearchResults search={contentSearch} onOpen={(match) => {
            if (!selectedDirectory) return;
            const relativePath = match.path;
            openDoc(sessionId ?? "", {
              kind: "workspace_file", id: `${selectedDirectory.taskId ?? selectedDirectory.key}:${relativePath}`, label: relativePath,
              sessionId: sessionId ?? undefined, projectId: projectID || undefined, taskId: selectedDirectory.taskId,
              projectResourceId: selectedDirectory.projectResourceId, runtimeId: selectedDirectory.runtimeId,
              workDir: selectedDirectory.path, relativePath,
              fileId: match.fileId, directoryId: match.directoryId,
            }, match.location);
          }} /> : showNoMatch ? (
            <div className="p-4 text-muted-foreground"><p className="text-body">{t(($) => $.doc_file_list.no_match)}</p></div>
          ) : visible.length > 0 ? (
            <ul className="flex-1 overflow-y-auto" aria-label={t(($) => $.doc_file_list.project_files)}>
              {renderTree(fileTree)}
            </ul>
          ) : (
            <div className="flex-1" />
          )}
          {!searchTerm && hasMore && <Button type="button" variant="ghost" size="sm" disabled={workspaceFiles.isFetching} onClick={() => void loadMore()}>{t(($) => $.doc_file_list.load_more)}</Button>}
        </div>
      )}
    </div>
  );
}
