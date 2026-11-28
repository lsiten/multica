import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { readReviewFile, readReviewManifest } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";
import { LocalReviewContent } from "./local-review-content";
import { LocalReviewContext } from "./local-review-context";
import {
  ChevronDown,
  ChevronRight,
  List,
  ListTree,
  Minus,
  Plus,
  Search,
  Sparkles,
  Type,
  WrapText,
  X,
} from "@multica/ui/icons";
import type { LocalIndexView } from "@multica/core/types/local-review-index";
import { CodeMirrorDiff, type CodeMirrorDiffHandle } from "../../editor";
import {
  reviewDirectoryGroups,
  reviewFileCode,
  reviewFileMatchesFilter,
  type ReviewDiffLayout,
  type ReviewFileFilter,
  type ReviewFileView,
  type ReviewFileRow,
} from "./local-review-workspace-state";
import {
  reviewCommentId,
  reviewFindingsForPatch,
  type LocalReviewComment,
  type ReviewCommentSide,
} from "./local-review-comments";

export type LocalReviewRowStaging = {
  readonly files: LocalIndexView["status"]["files"];
  readonly busy: boolean;
  readonly preview?: ReviewManifest;
  readonly previewError?: Error;
  readonly workingPreview?: ReviewManifest;
  readonly workingPreviewError?: Error;
  readonly reason?: string;
  readonly change: (action: "stage" | "unstage", path: string) => void;
};
export type LocalReviewFileSelection = { readonly files: readonly { readonly path: string }[]; readonly busy: boolean; readonly add: (file: ReviewManifest["page"]["files"][number]) => void; readonly remove: (path: string) => void; readonly addAll?: () => void; readonly removeAll?: () => void };

export function LocalReviewFileBrowser({ request, manifest, staging, selection, selectedPath, onSelectPath, diffLayout = "inherit", fileFilter = "all", fileView = "list", onFileFilterChange, onFileViewChange, comments = [], focusComment, commentSide = "new", onCommentSideChange, onAddComment, onFindings }: {
  request: Omit<PagedReviewInput, "action">;
  manifest: ReviewManifest;
  staging?: LocalReviewRowStaging;
  selection?: LocalReviewFileSelection;
  selectedPath?: string;
  onSelectPath?: (path: string) => void;
  diffLayout?: ReviewDiffLayout | "inherit";
  fileFilter?: ReviewFileFilter;
  fileView?: ReviewFileView;
  onFileFilterChange?: (filter: ReviewFileFilter) => void;
  onFileViewChange?: (view: ReviewFileView) => void;
  comments?: readonly LocalReviewComment[];
  focusComment?: LocalReviewComment | null;
  commentSide?: ReviewCommentSide;
  onCommentSideChange?: (side: ReviewCommentSide) => void;
  onAddComment?: (comment: LocalReviewComment) => void;
  onFindings?: (findings: readonly LocalReviewComment[]) => void;
}) {
  const { t } = useT("issues");
  const errorId = useId();
  const [selected, setSelected] = useState(selectedPath ?? "");
  const [stagedSelection, setStagedSelection] = useState("");
  const [branchSelection, setBranchSelection] = useState("");
  const [stagedVisible, setStagedVisible] = useState(100);
  const [workingVisible, setWorkingVisible] = useState(100);
  const [query, setQuery] = useState("");
  const [fileBrowserOpen, setFileBrowserOpen] = useState(true);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
  const [openTabs, setOpenTabs] = useState<readonly string[]>(selectedPath ? [selectedPath] : []);
  const searchRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const changedListRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef(new Map<string, HTMLButtonElement>());
  const files = useInfiniteQuery({
    queryKey: ["review-version-files", request.workspace_id, request.runtime_id, request.task_id, request.project_resource_id, request.path, request.scope, request.target, manifest.version_id],
    queryFn: ({ pageParam, signal }) => readReviewManifest({ ...request, target: manifest.header.target, action: "files", version_id: manifest.version_id, offset: pageParam, limit: 100 }, signal),
    initialPageParam: 0,
    initialData: { pages: [manifest], pageParams: [0] },
    getNextPageParam: (last) => last.page.has_more ? last.page.next_offset : undefined,
    staleTime: Infinity, refetchOnWindowFocus: false, retry: false, networkMode: "always",
  });
  const indexFiles = new Map(staging?.files.map((entry) => [entry.path, entry]));
  const stagedFiles = staging?.files.filter((entry) => entry.staged) ?? [];
  const allRows = files.data.pages.flatMap((page) => page.page.files);
  const pendingPaths = new Set(selection?.files.map((entry) => entry.path));
  const workingFiles = new Map(staging?.workingPreview?.page.files.map((entry) => [entry.path, entry]));
  const rows = allRows.filter((entry) => { const status = indexFiles.get(entry.path); return (!status?.staged || status.unstaged) && (!pendingPaths.has(entry.path) || status?.unstaged); }).map((entry) => indexFiles.get(entry.path)?.unstaged ? workingFiles.get(entry.path) ?? { ...entry, status: "working_only" } : entry);
  const listedPaths = new Set(rows.map((entry) => entry.path));
  for (const entry of staging?.files ?? []) {
    if (entry.unstaged && !listedPaths.has(entry.path)) { rows.push(workingFiles.get(entry.path) ?? { path: entry.path, status: "working_only", preview: "text", additions: 0, deletions: 0 }); listedPaths.add(entry.path); }
  }
  rows.sort((first, second) => Number(!!indexFiles.get(second.path)?.unstaged) - Number(!!indexFiles.get(first.path)?.unstaged));
  const statusRows: ReviewFileRow[] = rows.map((entry) => ({
    ...entry,
    indexFile: indexFiles.get(entry.path),
  }));
  const filteredRows = statusRows.filter((entry) =>
    reviewFileMatchesFilter(entry, fileFilter, query),
  );
  const effectiveSelected = selectedPath || selected;
  const rememberTab = (path: string) => {
    if (!path) return;
    setOpenTabs((tabs) => tabs.includes(path) ? tabs : [...tabs, path]);
  };
  const selectFile = (path: string) => {
    setSelected(path);
    rememberTab(path);
    onSelectPath?.(path);
  };
  const closeTab = (path: string) => {
    setOpenTabs((tabs) => {
      const next = tabs.filter((tab) => tab !== path);
      if (path === file?.path) {
        const fallback = next.at(-1);
        if (fallback) {
          setSelected(fallback);
          onSelectPath?.(fallback);
        }
      }
      return next;
    });
  };
  useEffect(() => {
    if (!selectedPath || selectedPath === selected) return;
    setSelected(selectedPath);
    rememberTab(selectedPath);
    // The path is owned by the workspace URL; local selection mirrors it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedPath]);
  useEffect(() => {
    const focusQuickOpen = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== "p") return;
      event.preventDefault();
      searchRef.current?.focus();
      searchRef.current?.select();
    };
    window.addEventListener("keydown", focusQuickOpen);
    return () => window.removeEventListener("keydown", focusQuickOpen);
  }, []);
  const visibleRows = Math.max(workingVisible, allRows.length, filteredRows.findIndex((entry) => entry.path === effectiveSelected) + 1);
  const activePath = effectiveSelected || filteredRows[0]?.path || allRows[0]?.path || "";
  useEffect(() => {
    if (!activePath) return;
    rowRefs.current.get(activePath)?.scrollIntoView?.({ block: "nearest" });
  }, [activePath]);
  const branchPath = stagedSelection && indexFiles.get(stagedSelection)?.staged ? "" : pendingPaths.has(branchSelection) ? branchSelection : pendingPaths.has(activePath) && !indexFiles.get(activePath)?.unstaged ? activePath : "";
  const selectBranch = (path: string) => { selectFile(path); setStagedSelection(""); setBranchSelection(path); };
  const pendingIndex = selection?.files.findIndex((entry) => entry.path === branchPath) ?? -1;
  const pendingNavigation = {
    loading: selection?.busy ?? false,
    previous: pendingIndex > 0 ? () => selectBranch(selection?.files[pendingIndex - 1]?.path ?? "") : undefined,
    next: selection?.files[pendingIndex + 1] ? () => selectBranch(selection.files[pendingIndex + 1]?.path ?? "") : stagedFiles[0] ? () => selectStaged(stagedFiles[0]?.path ?? "") : filteredRows[0] ? () => { selectFile(filteredRows[0]?.path ?? ""); setStagedSelection(""); setBranchSelection(""); } : undefined,
  };
  const stagedPath = stagedSelection && indexFiles.get(stagedSelection)?.staged ? stagedSelection : indexFiles.get(activePath)?.staged && !indexFiles.get(activePath)?.unstaged ? activePath : "";
  const stagedIndex = stagedFiles.findIndex((entry) => entry.path === stagedPath);
  const selectStaged = (path: string) => { const position = stagedFiles.findIndex((entry) => entry.path === path); if (position >= stagedVisible) setStagedVisible(position + 1); selectFile(path); setStagedSelection(path); setBranchSelection(""); };
  const stagedNavigation = {
    loading: staging?.busy ?? false,
    previous: stagedIndex > 0 ? () => selectStaged(stagedFiles[stagedIndex - 1]?.path ?? "") : selection?.files.length ? () => selectBranch(selection.files.at(-1)?.path ?? "") : undefined,
    next: stagedFiles[stagedIndex + 1] ? () => selectStaged(stagedFiles[stagedIndex + 1]?.path ?? "") : filteredRows[0] ? () => { selectFile(filteredRows[0]?.path ?? ""); setStagedSelection(""); } : undefined,
  };
  const file = filteredRows.find((entry) => entry.path === effectiveSelected) ?? filteredRows[0];
  useEffect(() => {
    const closeDiffTab = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "w") return;
      const target = event.target;
      if (target instanceof HTMLElement && (target.isContentEditable || ["INPUT", "TEXTAREA"].includes(target.tagName))) return;
      if (!file?.path) return;
      event.preventDefault();
      setOpenTabs((tabs) => {
        const next = tabs.filter((tab) => tab !== file.path);
        const fallback = next.at(-1);
        if (fallback) {
          setSelected(fallback);
          onSelectPath?.(fallback);
        }
        return next;
      });
    };
    window.addEventListener("keydown", closeDiffTab);
    return () => window.removeEventListener("keydown", closeDiffTab);
  }, [file?.path, onSelectPath]);
  const workingPath = file && indexFiles.get(file.path)?.unstaged ? file.path : "";
  const workingManifest = workingPath ? staging?.workingPreview : undefined;
  const fileIndex = filteredRows.findIndex((entry) => entry.path === file?.path);
  const nextFile = async () => {
    const next = filteredRows[fileIndex + 1];
    if (next) { selectFile(next.path); return; }
    if (!files.hasNextPage) return;
    const result = await files.fetchNextPage({ throwOnError: false });
    const available = result.data?.pages.flatMap((page) => page.page.files).filter((entry) => {
      const status = indexFiles.get(entry.path);
      return (!status?.staged || status.unstaged)
        && reviewFileMatchesFilter({ ...entry, indexFile: status }, fileFilter, query);
    }) ?? [];
    const position = available.findIndex((entry) => entry.path === file?.path);
    const loaded = position >= 0 ? available[position + 1] : undefined;
    if (loaded) selectFile(loaded.path);
  };
  const navigation = {
    previous: fileIndex > 0 ? () => selectFile(filteredRows[fileIndex - 1]?.path ?? "") : stagedFiles.length ? () => selectStaged(stagedFiles.at(-1)?.path ?? "") : selection?.files.length ? () => selectBranch(selection.files.at(-1)?.path ?? "") : undefined,
    next: filteredRows[fileIndex + 1] || files.hasNextPage ? () => { void nextFile(); } : undefined,
    loading: files.isFetchingNextPage,
  };
  const renderChangedFile = (entry: ReviewFileRow) => {
    const status = indexFiles.get(entry.path);
    const forMerge = !!selection && !status?.unstaged;
    const selectedRow = !stagedPath && !branchPath && file?.path === entry.path;
    return (
      <div key={entry.path} className={`group/file flex min-w-0 items-start ${selectedRow ? "bg-accent" : ""}`}>
        <button
          ref={(node) => {
            if (node) rowRefs.current.set(entry.path, node);
            else rowRefs.current.delete(entry.path);
          }}
          type="button"
          aria-pressed={selectedRow}
          title={entry.path}
          onClick={() => {
            selectFile(entry.path);
            setStagedSelection("");
            setBranchSelection("");
          }}
          className={`flex min-w-0 flex-1 items-start gap-2 px-2 py-1.5 text-left text-caption hover:bg-accent ${selectedRow ? "font-semibold" : ""}`}
        >
          <span className="mt-0.5 w-3 shrink-0 text-center font-mono text-micro text-muted-foreground">
            {reviewFileCode(entry)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate">{entry.path.split("/").at(-1)}</span>
            <span className="block truncate text-micro text-muted-foreground">{entry.path.includes("/") ? entry.path.slice(0, entry.path.lastIndexOf("/")) : "."}</span>
          </span>
          {entry.status !== "working_only" && (
            <span className="shrink-0 tabular-nums text-micro text-muted-foreground">
              <span className="text-success">+{entry.additions}</span>{" "}
              <span className="text-destructive">-{entry.deletions}</span>
            </span>
          )}
        </button>
        {(staging || selection) && (
          <div className="flex shrink-0 gap-1 pr-1 pt-1 opacity-0 transition-opacity group-hover/file:opacity-100 group-focus-within/file:opacity-100" title={forMerge ? t(($) => $.local_review.selection_hint) : staging?.reason || (!status ? t(($) => $.local_review.no_working_change) : status.conflicted || status.unsupported ? t(($) => $.local_review.index_unavailable) : undefined)}>
            <Button variant="ghost" size="icon-xs" aria-label={forMerge ? t(($) => $.local_review.add_selection, { path: entry.path }) : t(($) => $.local_review.stage_file, { path: entry.path })} disabled={forMerge ? selection?.busy || manifest.header.dirty : staging?.busy || !status?.unstaged || status.conflicted || status.unsupported} onClick={() => { if (forMerge) selection?.add(entry); else staging?.change("stage", entry.path); }}><Plus aria-hidden="true" /></Button>
          </div>
        )}
      </div>
    );
  };
  const treeGroups = reviewDirectoryGroups(filteredRows);
  const virtualizeChangedFiles = fileView === "list" && filteredRows.length > 500;
  const changedFilesVirtualizer = useVirtualizer({
    count: virtualizeChangedFiles ? filteredRows.length : 0,
    getScrollElement: () => changedListRef.current,
    estimateSize: () => 48,
    overscan: 8,
  });
  const treeItems: (
    | { readonly kind: "directory"; readonly directory: string; readonly count: number }
    | { readonly kind: "file"; readonly file: ReviewFileRow }
  )[] = treeGroups.flatMap((group) => [
    { kind: "directory" as const, directory: group.directory, count: group.files.length },
    ...(collapsed.has(group.directory)
      ? []
      : group.files.map((entry) => ({ kind: "file" as const, file: entry }))),
  ]);
  const virtualizeTree = fileView === "tree" && filteredRows.length > 500;
  const changedTreeVirtualizer = useVirtualizer({
    count: virtualizeTree ? treeItems.length : 0,
    getScrollElement: () => changedListRef.current,
    estimateSize: () => 32,
    overscan: 12,
  });
  return (
    <div className={`grid min-h-0 flex-1 grid-cols-1 ${fileBrowserOpen ? "grid-rows-[auto_minmax(0,1fr)] md:grid-cols-[17rem_minmax(0,1fr)] md:grid-rows-1" : ""}`}>
      {fileBrowserOpen && <nav id="local-review-changes" tabIndex={-1} className="flex max-h-60 min-h-0 flex-col overflow-hidden border-r bg-card outline-none md:max-h-none" aria-label={t(($) => $.local_review.files)}>
        <div className="shrink-0 space-y-2 border-b p-2">
          <div className="flex items-center justify-between gap-2">
            <span className="text-caption font-semibold">{t(($) => $.local_review.files)}</span>
            <Button size="icon-xs" variant="ghost" aria-label={t(($) => $.local_review.hide_files)} onClick={() => setFileBrowserOpen(false)}>
              <X aria-hidden="true" />
            </Button>
          </div>
          <label className="relative block">
            <Search className="pointer-events-none absolute left-2 top-2 size-3.5 text-muted-foreground" aria-hidden="true" />
            <Input
              ref={searchRef}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && filteredRows[0]) {
                  event.preventDefault();
                  selectFile(filteredRows[0].path);
                } else if (event.key === "Escape") {
                  event.preventDefault();
                  setQuery("");
                }
              }}
              placeholder={t(($) => $.local_review.filter_files)}
              className="h-8 pl-7 text-caption"
            />
          </label>
          <div className="flex items-center gap-1">
            <select
              aria-label={t(($) => $.local_review.file_filter)}
              value={fileFilter}
              onChange={(event) => onFileFilterChange?.(event.target.value as ReviewFileFilter)}
              className="h-7 min-w-0 flex-1 rounded-md border bg-background px-1.5 text-caption"
            >
              <option value="all">{t(($) => $.local_review.filter_all)}</option>
              <option value="modified">{t(($) => $.local_review.filter_modified)}</option>
              <option value="added">{t(($) => $.local_review.filter_added)}</option>
              <option value="deleted">{t(($) => $.local_review.filter_deleted)}</option>
              <option value="conflicts">{t(($) => $.local_review.filter_conflicts)}</option>
            </select>
            <Button size="icon-xs" variant={fileView === "list" ? "secondary" : "ghost"} aria-pressed={fileView === "list"} aria-label={t(($) => $.local_review.list_view)} onClick={() => onFileViewChange?.("list")}><List aria-hidden="true" /></Button>
            <Button size="icon-xs" variant={fileView === "tree" ? "secondary" : "ghost"} aria-pressed={fileView === "tree"} aria-label={t(($) => $.local_review.tree_view)} onClick={() => onFileViewChange?.("tree")}><ListTree aria-hidden="true" /></Button>
          </div>
        </div>
        <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto">
          {selection?.addAll && <div className="flex flex-wrap gap-2 border-b p-2" title={t(($) => $.local_review.selection_hint)}><Button variant="outline" size="xs" disabled={selection.busy || manifest.header.dirty || !manifest.page.total_files} onClick={selection.addAll}>{t(($) => $.local_review.add_all_selection)}</Button><Button variant="ghost" size="xs" disabled={selection.busy || !selection.files.length} onClick={selection.removeAll}>{t(($) => $.local_review.remove_all_selection)}</Button></div>}
          {selection && <div className="border-b"><h4 className="px-2 py-1.5 text-caption font-semibold">{t(($) => $.local_review.pending_merge)} · {selection.files.length}</h4>{selection.files.map((entry) => <div key={entry.path} className={`flex items-start ${branchPath === entry.path ? "bg-accent" : ""}`}><button type="button" aria-label={`${t(($) => $.local_review.pending_merge)} ${entry.path}`} aria-pressed={branchPath === entry.path} className="min-w-0 flex-1 break-all px-2 py-1.5 text-left text-caption" onClick={() => { selectFile(entry.path); setStagedSelection(""); setBranchSelection(entry.path); }}>{entry.path}</button><Button variant="ghost" size="icon-xs" className="mr-1 mt-1" aria-label={t(($) => $.local_review.remove_selection, { path: entry.path })} disabled={selection.busy} onClick={() => selection.remove(entry.path)}><Minus aria-hidden="true" /></Button></div>)}</div>}
          {staging && <div className="border-b"><h4 className="px-2 py-1.5 text-caption font-semibold">{t(($) => $.local_review.staged)} · {stagedFiles.length}</h4>{stagedFiles.slice(0, stagedVisible).map((entry) => <div key={entry.path} className={`flex items-start ${stagedPath === entry.path ? "bg-accent" : ""}`}><button type="button" aria-label={`${t(($) => $.local_review.staged)} ${entry.path}`} aria-pressed={stagedPath === entry.path} className="min-w-0 flex-1 break-all px-2 py-1.5 text-left text-caption hover:bg-accent" onClick={() => selectStaged(entry.path)}>{entry.path}</button><Button className="mr-1 mt-1" variant="ghost" size="icon-xs" aria-label={t(($) => $.local_review.unstage_file, { path: entry.path })} disabled={staging.busy || entry.conflicted || entry.unsupported} onClick={() => staging.change("unstage", entry.path)}><Minus aria-hidden="true" /></Button></div>)}</div>}
          {staging && stagedFiles.length > stagedVisible && <Button variant="ghost" className="m-2" onClick={() => setStagedVisible(stagedVisible + 100)}>{t(($) => $.local_review.staged)} · {t(($) => $.local_review.load_more_files)}</Button>}
          <div className="border-b px-2 py-1.5 text-caption font-semibold">{t(($) => $.local_review.branch_changes)} · {filteredRows.length}</div>
          {fileView === "tree" ? virtualizeTree ? (
            <div ref={changedListRef} className="h-[min(60vh,44rem)] min-h-72 overflow-auto">
              <div
                className="relative w-full"
                style={{ height: changedTreeVirtualizer.getTotalSize() }}
              >
                {changedTreeVirtualizer.getVirtualItems().map((item) => {
                  const entry = treeItems[item.index];
                  if (!entry) return null;
                  return (
                    <div
                      key={entry.kind === "directory" ? `directory:${entry.directory}` : entry.file.path}
                      ref={changedTreeVirtualizer.measureElement}
                      data-index={item.index}
                      className="absolute inset-x-0 top-0"
                      style={{ transform: `translateY(${item.start}px)` }}
                    >
                      {entry.kind === "directory" ? (
                        <button
                          type="button"
                          className="flex h-8 w-full items-center gap-1 px-2 text-left text-caption text-muted-foreground hover:bg-accent"
                          aria-expanded={!collapsed.has(entry.directory)}
                          onClick={() => setCollapsed((current) => {
                            const next = new Set(current);
                            if (next.has(entry.directory)) next.delete(entry.directory);
                            else next.add(entry.directory);
                            return next;
                          })}
                        >
                          {collapsed.has(entry.directory) ? <ChevronRight className="size-3" aria-hidden="true" /> : <ChevronDown className="size-3" aria-hidden="true" />}
                          <span className="truncate">{entry.directory || "."}</span>
                          <span className="ml-auto tabular-nums">{entry.count}</span>
                        </button>
                      ) : renderChangedFile(entry.file)}
                    </div>
                  );
                })}
              </div>
            </div>
          ) : treeGroups.map((group) => {
            const isCollapsed = collapsed.has(group.directory);
            return (
              <section key={group.directory || "."}>
                <button type="button" className="flex w-full items-center gap-1 px-2 py-1 text-left text-caption text-muted-foreground hover:bg-accent" aria-expanded={!isCollapsed} onClick={() => setCollapsed((current) => { const next = new Set(current); if (next.has(group.directory)) next.delete(group.directory); else next.add(group.directory); return next; })}>
                  {isCollapsed ? <ChevronRight className="size-3" aria-hidden="true" /> : <ChevronDown className="size-3" aria-hidden="true" />}
                  <span className="truncate">{group.directory || "."}</span>
                  <span className="ml-auto tabular-nums">{group.files.length}</span>
                </button>
                {!isCollapsed && group.files.map(renderChangedFile)}
              </section>
            );
          }) : virtualizeChangedFiles ? (
            <div ref={changedListRef} className="h-[min(60vh,44rem)] min-h-72 overflow-auto">
              <div className="relative w-full" style={{ height: changedFilesVirtualizer.getTotalSize() }}>
                {changedFilesVirtualizer.getVirtualItems().map((item) => {
                  const entry = filteredRows[item.index];
                  return entry ? (
                    <div
                      key={entry.path}
                      ref={changedFilesVirtualizer.measureElement}
                      data-index={item.index}
                      className="absolute inset-x-0 top-0"
                      style={{ transform: `translateY(${item.start}px)` }}
                    >
                      {renderChangedFile(entry)}
                    </div>
                  ) : null;
                })}
              </div>
            </div>
          ) : filteredRows.slice(0, visibleRows).map(renderChangedFile)}
          {filteredRows.length > visibleRows && <Button variant="ghost" className="m-2" onClick={() => setWorkingVisible(visibleRows + 100)}>{t(($) => $.local_review.unstaged)} · {t(($) => $.local_review.load_more_files)}</Button>}
          <p className="p-2 text-caption text-muted-foreground">{t(($) => $.local_review.branch_changes)} · {t(($) => $.local_review.loaded_files, { loaded: allRows.length, total: manifest.page.total_files })}</p>
          {files.error && <LocalReviewError error={files.error} id={errorId} />}
          {files.hasNextPage && <Button className="m-2" variant="outline" disabled={files.isFetchingNextPage} onClick={() => void files.fetchNextPage()}>{t(($) => $.local_review.load_more_files)}</Button>}
        </div>
      </nav>}
      <div className="flex min-h-0 min-w-0 flex-col">
        {(!!openTabs.length || !fileBrowserOpen) && <div className="flex min-h-9 shrink-0 items-end gap-1 overflow-x-auto border-b bg-muted/20 px-1 pt-1">
          {!fileBrowserOpen && <Button className="mb-0.5 shrink-0" size="xs" variant="ghost" aria-label={t(($) => $.local_review.show_files)} onClick={() => setFileBrowserOpen(true)}><ListTree aria-hidden="true" />{t(($) => $.local_review.files)}</Button>}
          {openTabs.map((path) => (
            <div key={path} className={`flex max-w-56 shrink-0 items-center rounded-t-md border border-b-0 ${file?.path === path ? "bg-background font-medium" : "bg-muted/40 text-muted-foreground"}`}>
              <button type="button" className="flex min-w-0 items-center gap-2 py-1.5 pl-2 pr-1 text-caption" title={path} onClick={() => selectFile(path)}>
                <span className="truncate">{path.split("/").at(-1)}</span>
              </button>
              <button type="button" className="mr-1 rounded-sm p-0.5 hover:bg-accent focus-visible:ring-2" aria-label={t(($) => $.local_review.close_file_tab, { path })} onClick={() => closeTab(path)}>
                <X className="size-3" aria-hidden="true" />
              </button>
            </div>
          ))}
        </div>}
        {branchPath ? <LocalReviewPatch key={manifest.version_id + branchPath} request={request} manifest={manifest} filePath={branchPath} navigation={pendingNavigation} layout={diffLayout} comments={comments} focusComment={focusComment} commentSide={commentSide} onCommentSideChange={onCommentSideChange} onAddComment={onAddComment} onFindings={onFindings} /> : stagedPath ? staging?.preview ? <LocalReviewPatch key={staging.preview.version_id + stagedPath} request={request} manifest={staging.preview} filePath={stagedPath} navigation={stagedNavigation} layout={diffLayout} comments={comments} focusComment={focusComment} commentSide={commentSide} onCommentSideChange={onCommentSideChange} onAddComment={onAddComment} onFindings={onFindings} /> : staging?.previewError ? <LocalReviewError error={staging.previewError} id={errorId} /> : <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p> : workingPath ? workingManifest ? <LocalReviewPatch key={workingManifest.version_id + workingPath} request={request} manifest={workingManifest} filePath={workingPath} navigation={navigation} layout={diffLayout} comments={comments} focusComment={focusComment} commentSide={commentSide} onCommentSideChange={onCommentSideChange} onAddComment={onAddComment} onFindings={onFindings} /> : staging?.workingPreviewError ? <LocalReviewError error={staging.workingPreviewError} id={errorId} /> : <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p> : file ? <LocalReviewPatch key={manifest.version_id + file.path} request={request} manifest={manifest} filePath={file.path} navigation={navigation} layout={diffLayout} comments={comments} focusComment={focusComment} commentSide={commentSide} onCommentSideChange={onCommentSideChange} onAddComment={onAddComment} onFindings={onFindings} /> : <p className="p-3 text-caption">{t(($) => $.local_review.empty)}</p>}
      </div>
    </div>
  );
}

export type LocalReviewFileNavigation = { readonly previous?: () => void; readonly next?: () => void; readonly loading: boolean };

function LocalReviewPatch({ request, manifest, filePath, navigation, layout, comments, focusComment, commentSide, onCommentSideChange, onAddComment, onFindings }: {
  request: Omit<PagedReviewInput, "action">;
  manifest: ReviewManifest;
  filePath: string;
  navigation: LocalReviewFileNavigation;
  layout: ReviewDiffLayout | "inherit";
  comments: readonly LocalReviewComment[];
  focusComment?: LocalReviewComment | null;
  commentSide: ReviewCommentSide;
  onCommentSideChange?: (side: ReviewCommentSide) => void;
  onAddComment?: (comment: LocalReviewComment) => void;
  onFindings?: (findings: readonly LocalReviewComment[]) => void;
}) {
  const { t } = useT("issues");
  const errorId = useId();
  const [offsets, setOffsets] = useState([0]);
  const [index, setIndex] = useState(0);
  const [showContent, setShowContent] = useState(false);
  const [showDiff, setShowDiff] = useState(true);
  const [commentOpen, setCommentOpen] = useState(false);
  const [commentLine, setCommentLine] = useState(1);
  const [commentDraft, setCommentDraft] = useState("");
  const [wrap, setWrap] = useState(true);
  const [showWhitespace, setShowWhitespace] = useState(false);
  const [lineTarget, setLineTarget] = useState("");
  const diffRef = useRef<CodeMirrorDiffHandle>(null);
  const offset = offsets[index] ?? 0;
  const query = useQuery({
    queryKey: ["review-version-patch", request.workspace_id, request.runtime_id, request.task_id, request.project_resource_id, request.path, request.scope, request.target, manifest.version_id, filePath, offset],
    queryFn: ({ signal }) => readReviewFile({ ...request, target: manifest.header.target, action: "file", version_id: manifest.version_id, file_path: filePath, offset, limit: 200 }, signal),
    enabled: !showContent,
    retry: false, staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false, networkMode: "always",
  });
  const response = query.data;
  const messages = { binary: t(($) => $.local_review.binary_file), too_large: t(($) => $.local_review.large_file), uncached: t(($) => $.local_review.uncached_file), unsupported: t(($) => $.local_review.unsupported_file) };
  const diffLines = useMemo(
    () => response?.page?.lines.filter((line) => line.kind !== "meta") ?? [],
    [response?.page?.lines],
  );
  const original = diffLines.filter((line) => line.kind !== "add").map((line) => line.text).join("\n");
  const modified = diffLines.filter((line) => line.kind !== "remove").map((line) => line.text).join("\n");
  const oldLineNumbers = useMemo(
    () => diffLines.filter((line) => line.kind !== "add").map((line) => line.old_line),
    [diffLines],
  );
  const newLineNumbers = useMemo(
    () => diffLines.filter((line) => line.kind !== "remove").map((line) => line.new_line),
    [diffLines],
  );
  const contextRows = useMemo(
    () => response?.page?.lines.filter((line) =>
      line.kind === "meta" &&
      !!line.context_lines &&
      !!line.context_old_start &&
      !!line.context_new_start
    ) ?? [],
    [response?.page?.lines],
  );
  const fileComments = useMemo(
    () => comments.filter((comment) =>
      comment.file === filePath && comment.revision === manifest.version_id
    ),
    [comments, filePath, manifest.version_id],
  );
  const annotations = useMemo(
    () => {
      const positions = new Map(
        newLineNumbers.flatMap((line, index) =>
          line === undefined ? [] : [[line, index + 1] as const],
        ),
      );
      return fileComments
        .filter((comment) => comment.side === "new")
        .flatMap((comment) => {
          const line = positions.get(comment.line);
          return line === undefined
            ? []
            : [{ line, resolved: comment.resolved }];
        });
    },
    [fileComments, newLineNumbers],
  );
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        (target.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))
      ) {
        return;
      }
      if (event.key === "F7") {
        event.preventDefault();
        if (event.shiftKey) diffRef.current?.previousChange();
        else diffRef.current?.nextChange();
      } else if (event.altKey && event.key === "ArrowDown" && navigation.next) {
        event.preventDefault();
        navigation.next();
      } else if (event.altKey && event.key === "ArrowUp" && navigation.previous) {
        event.preventDefault();
        navigation.previous();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [navigation]);
  useEffect(() => {
    if (!focusComment || focusComment.file !== filePath) return;
    const lineNumbers =
      focusComment.side === "old" ? oldLineNumbers : newLineNumbers;
    if (!lineNumbers.includes(focusComment.line)) {
      if (response?.page?.has_more) {
        const nextOffset = response.page.next_line;
        if (nextOffset !== offset) {
          setOffsets([...offsets.slice(0, index + 1), nextOffset]);
          setIndex(index + 1);
        }
      }
      if (response?.page?.has_more) return;
    }
    const frame = window.requestAnimationFrame(() => {
      diffRef.current?.goToLine(focusComment.line, focusComment.side);
    });
    return () => window.cancelAnimationFrame(frame);
  }, [
    filePath,
    focusComment,
    index,
    newLineNumbers,
    offset,
    offsets,
    oldLineNumbers,
    response?.page?.has_more,
    response?.page?.next_line,
  ]);
  if (showContent) return <LocalReviewContent request={request} manifest={manifest} filePath={filePath} onBack={() => setShowContent(false)} navigation={navigation} />;
  return <section className="flex min-h-0 flex-col overflow-hidden rounded-sm border">
    <header className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card p-2"><p className="min-w-0 flex-1 break-all font-mono text-caption font-semibold">{filePath}</p>{response?.page && <Button variant={showDiff ? "secondary" : "ghost"} aria-pressed={showDiff} onClick={() => setShowDiff((visible) => !visible)}>{t(($) => showDiff ? $.local_review.patch_view : $.local_review.diff_view)}</Button>}{showDiff && response?.page && <><Button size="icon-xs" variant={wrap ? "secondary" : "ghost"} aria-label={t(($) => $.local_review.wrap_lines)} aria-pressed={wrap} onClick={() => setWrap((value) => !value)}><WrapText aria-hidden="true" /></Button><Button size="icon-xs" variant={showWhitespace ? "secondary" : "ghost"} aria-label={t(($) => $.local_review.show_whitespace)} aria-pressed={showWhitespace} onClick={() => setShowWhitespace((value) => !value)}><Type aria-hidden="true" /></Button><form className="flex items-center gap-1" onSubmit={(event) => { event.preventDefault(); const line = Number(lineTarget); if (line > 0) diffRef.current?.goToLine(line); }}><Input type="number" min={1} value={lineTarget} onChange={(event) => setLineTarget(event.target.value)} aria-label={t(($) => $.local_review.jump_to_line)} placeholder={t(($) => $.local_review.line_number)} className="h-7 w-20" /><Button type="submit" size="xs" variant="ghost" disabled={!lineTarget}>{t(($) => $.local_review.go)}</Button></form></>}{response?.page && onFindings && <Button variant="ghost" onClick={() => { const page = response?.page; if (page) onFindings?.(reviewFindingsForPatch({ file: filePath, revision: manifest.version_id, page })); }}><Sparkles aria-hidden="true" />{t(($) => $.local_review.find_issues)}</Button>}{response?.page && onAddComment && <Button variant="ghost" onClick={() => { setCommentLine(commentSide === "old" ? diffRef.current?.selectedOldLine() ?? 1 : diffRef.current?.selectedNewLine() ?? 1); setCommentOpen(true); }}>{t(($) => $.local_review.add_line_comment)}</Button>}<Button variant="ghost" disabled={query.isPending || response?.preview === "unsupported"} onClick={() => setShowContent(true)}>{t(($) => $.local_review.view_content)}</Button></header>
    <div key={offset} data-review-diff-scroll="" className="min-h-0 flex-1 overflow-auto">
      {query.isPending && <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p>}
      {query.error && <div className="p-3"><LocalReviewError error={query.error} id={errorId} /><Button className="mt-2" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.local_review.refresh)}</Button></div>}
      {response && response.preview !== "text" && <p role="status" className="p-3 text-caption text-muted-foreground">{messages[response.preview]}</p>}
      {response?.page && showDiff && (
        <div className="flex h-full min-h-[20rem] flex-col">
          {contextRows.map((line, row) => (
            <LocalReviewContext
              key={`${row}:${line.context_old_start}:${line.context_new_start}`}
              request={{ ...request, target: manifest.header.target }}
              versionId={manifest.version_id}
              filePath={filePath}
              oldStart={line.context_old_start ?? 1}
              newStart={line.context_new_start ?? 1}
              count={line.context_lines ?? 0}
            />
          ))}
          <div className="min-h-0 flex-1">
            <CodeMirrorDiff ref={diffRef} path={filePath} layout={layout === "inherit" ? "split" : layout} original={original} modified={modified} annotations={annotations} newLineNumbers={newLineNumbers} oldLineNumbers={oldLineNumbers} wrap={wrap} showWhitespace={showWhitespace} />
          </div>
        </div>
      )}
      {response?.page && !showDiff && <pre className="min-w-max p-2 font-mono text-caption">{response.page.lines.map((line, row) => <div key={offset + row}>
        {!!line.context_lines && !!line.context_old_start && !!line.context_new_start && <LocalReviewContext request={{ ...request, target: manifest.header.target }} versionId={manifest.version_id} filePath={filePath} oldStart={line.context_old_start} newStart={line.context_new_start} count={line.context_lines} />}
        <div className={line.kind === "add" ? "bg-success/10 text-success" : line.kind === "remove" ? "bg-destructive/10 text-destructive" : ""}><span className="mr-2 inline-block w-10 select-none text-right text-muted-foreground">{line.old_line ?? ""}</span><span className="mr-3 inline-block w-10 select-none text-right text-muted-foreground">{line.new_line ?? ""}</span>{line.text || " "}</div>
      </div>)}</pre>}
      {commentOpen && (
        <form
          className="m-2 space-y-2 rounded-md border bg-muted/20 p-2"
          onSubmit={(event) => {
            event.preventDefault();
            const body = commentDraft.trim();
            if (!body || !onAddComment) return;
            const line = commentLine;
            onAddComment({
              id: reviewCommentId({
                file: filePath,
                revision: manifest.version_id,
                side: commentSide,
                line,
                body,
              }),
              file: filePath,
              revision: manifest.version_id,
              side: commentSide,
              line,
              body,
              resolved: false,
              author: "member",
              replies: [],
            });
            setCommentDraft("");
            setCommentOpen(false);
          }}
        >
          <div className="flex flex-wrap items-center justify-between gap-2 text-caption">
            <span>{t(($) => $.local_review.comment_on_line, { line: commentLine })} · {commentSide === "old" ? t(($) => $.local_review.side_old) : t(($) => $.local_review.side_new)}</span>
            <div className="flex items-center gap-2">
              <select
                aria-label={t(($) => $.local_review.comment_side)}
                value={commentSide}
                onChange={(event) => onCommentSideChange?.(event.target.value as ReviewCommentSide)}
                className="h-7 rounded-md border bg-background px-1.5"
              >
                <option value="new">{t(($) => $.local_review.side_new)}</option>
                <option value="old">{t(($) => $.local_review.side_old)}</option>
              </select>
              <label className="flex items-center gap-1">
                <span>{t(($) => $.local_review.line)}</span>
                <Input
                  type="number"
                  min={1}
                  value={commentLine}
                  onChange={(event) => setCommentLine(Math.max(1, Number(event.target.value) || 1))}
                  className="h-7 w-20"
                />
              </label>
            </div>
          </div>
          <Textarea
            autoFocus
            rows={3}
            maxLength={4000}
            value={commentDraft}
            onChange={(event) => setCommentDraft(event.target.value)}
            placeholder={t(($) => $.local_review.line_comment_placeholder)}
          />
          <div className="flex justify-end gap-2">
            <Button type="button" size="sm" variant="ghost" onClick={() => setCommentOpen(false)}>{t(($) => $.local_review.cancel)}</Button>
            <Button type="submit" size="sm" disabled={!commentDraft.trim()}>{t(($) => $.local_review.add_comment)}</Button>
          </div>
        </form>
      )}
      {!!fileComments.length && (
        <div className="mx-2 mb-2 space-y-1 rounded-md border bg-card p-2 text-caption">
          {fileComments.map((comment) => (
            <button key={comment.id} type="button" className="block w-full rounded-sm px-1 py-0.5 text-left hover:bg-accent" onClick={() => { onCommentSideChange?.(comment.side); setCommentLine(comment.line); setCommentOpen(true); diffRef.current?.goToLine(comment.line, comment.side); }}>
              <span className={`font-medium ${comment.resolved ? "line-through text-muted-foreground" : ""}`}>{t(($) => $.local_review.comment_on_line, { line: comment.line })} · {comment.side === "old" ? t(($) => $.local_review.side_old) : t(($) => $.local_review.side_new)}</span>
              <span className="ml-2 text-muted-foreground">{comment.body}</span>
            </button>
          ))}
        </div>
      )}
    </div>
    <div className="flex shrink-0 items-center justify-between gap-2 border-t p-2">
      <Button variant="outline" disabled={(index === 0 && !navigation.previous) || query.isFetching || navigation.loading} onClick={() => { if (index > 0) setIndex(index - 1); else navigation.previous?.(); }}>{t(($) => $.local_review.previous_patch_page)}</Button>
      <span className="text-caption text-muted-foreground">{t(($) => $.local_review.patch_page, { page: index + 1 })}</span>
      <Button variant="outline" disabled={(!response?.page?.has_more && !navigation.next) || query.isFetching || navigation.loading} onClick={() => { if (response?.page?.has_more) { setOffsets([...offsets.slice(0, index + 1), response.page.next_line]); setIndex(index + 1); } else navigation.next?.(); }}>{t(($) => $.local_review.next_patch_page)}</Button>
    </div>
  </section>;
}
