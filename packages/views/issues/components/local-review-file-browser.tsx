import { useId, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { Button } from "@multica/ui/components/ui/button";
import { readReviewFile, readReviewManifest } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";
import { LocalReviewContent } from "./local-review-content";
import { LocalReviewContext } from "./local-review-context";
import { Plus, Minus } from "lucide-react";
import type { LocalIndexView } from "@multica/core/types/local-review-index";

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

export function LocalReviewFileBrowser({ request, manifest, staging, selection }: { request: LocalReviewRequest; manifest: ReviewManifest; staging?: LocalReviewRowStaging; selection?: LocalReviewFileSelection }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [selected, setSelected] = useState("");
  const [stagedSelection, setStagedSelection] = useState("");
  const [branchSelection, setBranchSelection] = useState("");
  const [stagedVisible, setStagedVisible] = useState(100);
  const [workingVisible, setWorkingVisible] = useState(100);
  const files = useInfiniteQuery({
    queryKey: ["review-version-files", request.workspace_id, request.task_id, manifest.version_id],
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
  const visibleRows = Math.max(workingVisible, allRows.length, rows.findIndex((entry) => entry.path === selected) + 1);
  const activePath = selected || allRows[0]?.path || "";
  const branchPath = stagedSelection && indexFiles.get(stagedSelection)?.staged ? "" : pendingPaths.has(branchSelection) ? branchSelection : pendingPaths.has(activePath) && !indexFiles.get(activePath)?.unstaged ? activePath : "";
  const selectBranch = (path: string) => { setSelected(path); setStagedSelection(""); setBranchSelection(path); };
  const pendingIndex = selection?.files.findIndex((entry) => entry.path === branchPath) ?? -1;
  const pendingNavigation = {
    loading: selection?.busy ?? false,
    previous: pendingIndex > 0 ? () => selectBranch(selection?.files[pendingIndex - 1]?.path ?? "") : undefined,
    next: selection?.files[pendingIndex + 1] ? () => selectBranch(selection.files[pendingIndex + 1]?.path ?? "") : stagedFiles[0] ? () => selectStaged(stagedFiles[0]?.path ?? "") : rows[0] ? () => { setSelected(rows[0]?.path ?? ""); setStagedSelection(""); setBranchSelection(""); } : undefined,
  };
  const stagedPath = stagedSelection && indexFiles.get(stagedSelection)?.staged ? stagedSelection : indexFiles.get(activePath)?.staged && !indexFiles.get(activePath)?.unstaged ? activePath : "";
  const stagedIndex = stagedFiles.findIndex((entry) => entry.path === stagedPath);
  const selectStaged = (path: string) => { const position = stagedFiles.findIndex((entry) => entry.path === path); if (position >= stagedVisible) setStagedVisible(position + 1); setSelected(path); setStagedSelection(path); setBranchSelection(""); };
  const stagedNavigation = {
    loading: staging?.busy ?? false,
    previous: stagedIndex > 0 ? () => selectStaged(stagedFiles[stagedIndex - 1]?.path ?? "") : selection?.files.length ? () => selectBranch(selection.files.at(-1)?.path ?? "") : undefined,
    next: stagedFiles[stagedIndex + 1] ? () => selectStaged(stagedFiles[stagedIndex + 1]?.path ?? "") : rows[0] ? () => { setSelected(rows[0]?.path ?? ""); setStagedSelection(""); } : undefined,
  };
  const file = rows.find((entry) => entry.path === selected) ?? rows[0];
  const workingPath = file && indexFiles.get(file.path)?.unstaged ? file.path : "";
  const workingManifest = workingPath ? staging?.workingPreview : undefined;
  const fileIndex = rows.findIndex((entry) => entry.path === file?.path);
  const nextFile = async () => {
    const next = rows[fileIndex + 1];
    if (next) { setSelected(next.path); return; }
    if (!files.hasNextPage) return;
    const result = await files.fetchNextPage({ throwOnError: false });
    const available = result.data?.pages.flatMap((page) => page.page.files).filter((entry) => { const status = indexFiles.get(entry.path); return !status?.staged || status.unstaged; }) ?? [];
    const position = available.findIndex((entry) => entry.path === file?.path);
    const loaded = position >= 0 ? available[position + 1] : undefined;
    if (loaded) setSelected((current) => current === selected ? loaded.path : current);
  };
  const navigation = {
    previous: fileIndex > 0 ? () => setSelected(rows[fileIndex - 1]?.path ?? "") : stagedFiles.length ? () => selectStaged(stagedFiles.at(-1)?.path ?? "") : selection?.files.length ? () => selectBranch(selection.files.at(-1)?.path ?? "") : undefined,
    next: rows[fileIndex + 1] || files.hasNextPage ? () => { void nextFile(); } : undefined,
    loading: files.isFetchingNextPage,
  };
  return <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] gap-3 md:grid-cols-[16rem_1fr] md:grid-rows-1">
    <nav className="max-h-36 overflow-auto rounded border md:max-h-none" aria-label={t(($) => $.local_review.files)}>
      {selection?.addAll && <div className="flex flex-wrap gap-2 border-b p-2" title={t(($) => $.local_review.selection_hint)}><Button variant="outline" size="xs" disabled={selection.busy || manifest.header.dirty || !manifest.page.total_files} onClick={selection.addAll}>{t(($) => $.local_review.add_all_selection)}</Button><Button variant="ghost" size="xs" disabled={selection.busy || !selection.files.length} onClick={selection.removeAll}>{t(($) => $.local_review.remove_all_selection)}</Button></div>}
      {selection && <div className="border-b"><h4 className="px-3 py-2 text-caption font-semibold">{t(($) => $.local_review.pending_merge)} · {selection.files.length}</h4>{selection.files.map((entry) => <div key={entry.path} className={`flex items-start ${branchPath === entry.path ? "bg-accent" : ""}`}><button type="button" aria-label={`${t(($) => $.local_review.pending_merge)} ${entry.path}`} aria-pressed={branchPath === entry.path} className="min-w-0 flex-1 break-all px-3 py-2 text-left text-caption" onClick={() => { setSelected(entry.path); setStagedSelection(""); setBranchSelection(entry.path); }}>{entry.path}</button><Button variant="ghost" size="icon-xs" className="mr-2 mt-2" aria-label={t(($) => $.local_review.remove_selection, { path: entry.path })} disabled={selection.busy} onClick={() => selection.remove(entry.path)}><Minus aria-hidden="true" /></Button></div>)}</div>}
      {staging && <div className="border-b"><h4 className="px-3 py-2 text-caption font-semibold">{t(($) => $.local_review.staged)} · {stagedFiles.length}</h4>{stagedFiles.slice(0, stagedVisible).map((entry) => <div key={entry.path} className={`flex items-start ${stagedPath === entry.path ? "bg-accent" : ""}`}><button type="button" aria-label={`${t(($) => $.local_review.staged)} ${entry.path}`} aria-pressed={stagedPath === entry.path} className="min-w-0 flex-1 break-all px-3 py-2 text-left text-caption hover:bg-accent" onClick={() => selectStaged(entry.path)}>{entry.path}</button><Button className="mr-2 mt-2" variant="ghost" size="icon-xs" aria-label={t(($) => $.local_review.unstage_file, { path: entry.path })} disabled={staging.busy || entry.conflicted || entry.unsupported} onClick={() => staging.change("unstage", entry.path)}><Minus aria-hidden="true" /></Button></div>)}</div>}
      {staging && stagedFiles.length > stagedVisible && <Button variant="ghost" className="m-2" onClick={() => setStagedVisible(stagedVisible + 100)}>{t(($) => $.local_review.staged)} · {t(($) => $.local_review.load_more_files)}</Button>}
      {rows.slice(0, visibleRows).map((entry) => { const status = indexFiles.get(entry.path); const forMerge = !!selection && !status?.unstaged; return <div key={entry.path} className={`flex items-start ${!stagedPath && !branchPath && file?.path === entry.path ? "bg-accent" : ""}`}><button type="button" aria-pressed={!stagedPath && !branchPath && file?.path === entry.path} onClick={() => { setSelected(entry.path); setStagedSelection(""); setBranchSelection(""); }} className={`block min-w-0 flex-1 break-all px-3 py-2 text-left text-caption hover:bg-accent ${!stagedPath && !branchPath && file?.path === entry.path ? "font-semibold" : ""}`}>
        <span>{entry.path}</span>
        {entry.status !== "working_only" && <span className="mt-1 block text-muted-foreground">{t(($) => $.local_review.file_changes, { additions: entry.additions, deletions: entry.deletions })}</span>}
      </button>{(staging || selection) && <div className="flex shrink-0 gap-1 pr-2 pt-2" title={forMerge ? t(($) => $.local_review.selection_hint) : staging?.reason || (!status ? t(($) => $.local_review.no_working_change) : status.conflicted || status.unsupported ? t(($) => $.local_review.index_unavailable) : undefined)}>
        <Button variant="ghost" size="icon-xs" aria-label={forMerge ? t(($) => $.local_review.add_selection, { path: entry.path }) : t(($) => $.local_review.stage_file, { path: entry.path })} disabled={forMerge ? selection?.busy || manifest.header.dirty : staging?.busy || !status?.unstaged || status.conflicted || status.unsupported} onClick={() => { if (forMerge) selection?.add(entry); else staging?.change("stage", entry.path); }}><Plus aria-hidden="true" /></Button>
      </div>}</div>; })}
      {rows.length > visibleRows && <Button variant="ghost" className="m-2" onClick={() => setWorkingVisible(visibleRows + 100)}>{t(($) => $.local_review.unstaged)} · {t(($) => $.local_review.load_more_files)}</Button>}
      <p className="p-2 text-caption text-muted-foreground">{t(($) => $.local_review.branch_changes)} · {t(($) => $.local_review.loaded_files, { loaded: allRows.length, total: manifest.page.total_files })}</p>
      {files.error && <LocalReviewError error={files.error} id={errorId} />}
      {files.hasNextPage && <Button className="m-2" variant="outline" disabled={files.isFetchingNextPage} onClick={() => void files.fetchNextPage()}>{t(($) => $.local_review.load_more_files)}</Button>}
    </nav>
    {branchPath ? <LocalReviewPatch key={manifest.version_id + branchPath} request={request} manifest={manifest} filePath={branchPath} navigation={pendingNavigation} /> : stagedPath ? staging?.preview ? <LocalReviewPatch key={staging.preview.version_id + stagedPath} request={request} manifest={staging.preview} filePath={stagedPath} navigation={stagedNavigation} /> : staging?.previewError ? <LocalReviewError error={staging.previewError} id={errorId} /> : <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p> : workingPath ? workingManifest ? <LocalReviewPatch key={workingManifest.version_id + workingPath} request={request} manifest={workingManifest} filePath={workingPath} navigation={navigation} /> : staging?.workingPreviewError ? <LocalReviewError error={staging.workingPreviewError} id={errorId} /> : <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p> : file ? <LocalReviewPatch key={manifest.version_id + file.path} request={request} manifest={manifest} filePath={file.path} navigation={navigation} /> : <p className="p-3 text-caption">{t(($) => $.local_review.empty)}</p>}
  </div>;
}

export type LocalReviewFileNavigation = { readonly previous?: () => void; readonly next?: () => void; readonly loading: boolean };

function LocalReviewPatch({ request, manifest, filePath, navigation }: { request: LocalReviewRequest; manifest: ReviewManifest; filePath: string; navigation: LocalReviewFileNavigation }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [offsets, setOffsets] = useState([0]);
  const [index, setIndex] = useState(0);
  const [showContent, setShowContent] = useState(false);
  const offset = offsets[index] ?? 0;
  const query = useQuery({
    queryKey: ["review-version-patch", request.workspace_id, request.task_id, manifest.version_id, filePath, offset],
    queryFn: ({ signal }) => readReviewFile({ ...request, target: manifest.header.target, action: "file", version_id: manifest.version_id, file_path: filePath, offset, limit: 200 }, signal),
    enabled: !showContent,
    retry: false, staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false, networkMode: "always",
  });
  const response = query.data;
  const messages = { binary: t(($) => $.local_review.binary_file), too_large: t(($) => $.local_review.large_file), uncached: t(($) => $.local_review.uncached_file), unsupported: t(($) => $.local_review.unsupported_file) };
  if (showContent) return <LocalReviewContent request={request} manifest={manifest} filePath={filePath} onBack={() => setShowContent(false)} navigation={navigation} />;
  return <section className="flex min-h-0 flex-col overflow-hidden rounded border">
    <header className="flex shrink-0 items-center gap-2 border-b bg-card p-2"><p className="min-w-0 flex-1 break-all font-mono text-caption font-semibold">{filePath}</p><Button variant="ghost" disabled={query.isPending || response?.preview === "unsupported"} onClick={() => setShowContent(true)}>{t(($) => $.local_review.view_content)}</Button></header>
    <div key={offset} className="min-h-0 flex-1 overflow-auto">
      {query.isPending && <p role="status" className="p-3 text-caption">{t(($) => $.local_review.loading)}</p>}
      {query.error && <div className="p-3"><LocalReviewError error={query.error} id={errorId} /><Button className="mt-2" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.local_review.refresh)}</Button></div>}
      {response && response.preview !== "text" && <p role="status" className="p-3 text-caption text-muted-foreground">{messages[response.preview]}</p>}
      {response?.page && <pre className="min-w-max p-2 font-mono text-caption">{response.page.lines.map((line, row) => <div key={offset + row}>
        {!!line.context_lines && !!line.context_old_start && !!line.context_new_start && <LocalReviewContext request={{ ...request, target: manifest.header.target }} versionId={manifest.version_id} filePath={filePath} oldStart={line.context_old_start} newStart={line.context_new_start} count={line.context_lines} />}
        <div className={line.kind === "add" ? "bg-success/10 text-success" : line.kind === "remove" ? "bg-destructive/10 text-destructive" : ""}><span className="mr-2 inline-block w-10 select-none text-right text-muted-foreground">{line.old_line ?? ""}</span><span className="mr-3 inline-block w-10 select-none text-right text-muted-foreground">{line.new_line ?? ""}</span>{line.text || " "}</div>
      </div>)}</pre>}
    </div>
    <div className="flex shrink-0 items-center justify-between gap-2 border-t p-2">
      <Button variant="outline" disabled={(index === 0 && !navigation.previous) || query.isFetching || navigation.loading} onClick={() => { if (index > 0) setIndex(index - 1); else navigation.previous?.(); }}>{t(($) => $.local_review.previous_patch_page)}</Button>
      <span className="text-caption text-muted-foreground">{t(($) => $.local_review.patch_page, { page: index + 1 })}</span>
      <Button variant="outline" disabled={(!response?.page?.has_more && !navigation.next) || query.isFetching || navigation.loading} onClick={() => { if (response?.page?.has_more) { setOffsets([...offsets.slice(0, index + 1), response.page.next_line]); setIndex(index + 1); } else navigation.next?.(); }}>{t(($) => $.local_review.next_patch_page)}</Button>
    </div>
  </section>;
}
