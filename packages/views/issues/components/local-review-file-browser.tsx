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

export function LocalReviewFileBrowser({ request, manifest }: { request: LocalReviewRequest; manifest: ReviewManifest }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [selected, setSelected] = useState("");
  const files = useInfiniteQuery({
    queryKey: ["review-version-files", request.workspace_id, request.task_id, manifest.version_id],
    queryFn: ({ pageParam, signal }) => readReviewManifest({ ...request, target: manifest.header.target, action: "files", version_id: manifest.version_id, offset: pageParam, limit: 100 }, signal),
    initialPageParam: 0,
    initialData: { pages: [manifest], pageParams: [0] },
    getNextPageParam: (last) => last.page.has_more ? last.page.next_offset : undefined,
    staleTime: Infinity, refetchOnWindowFocus: false, retry: false, networkMode: "always",
  });
  const rows = files.data.pages.flatMap((page) => page.page.files);
  const file = rows.find((entry) => entry.path === selected) ?? rows[0];
  const fileIndex = rows.findIndex((entry) => entry.path === file?.path);
  const nextFile = async () => {
    const next = rows[fileIndex + 1];
    if (next) { setSelected(next.path); return; }
    if (!files.hasNextPage) return;
    const result = await files.fetchNextPage({ throwOnError: false });
    const loaded = result.data?.pages.flatMap((page) => page.page.files)[fileIndex + 1];
    if (loaded) setSelected((current) => current === selected ? loaded.path : current);
  };
  const navigation = {
    previous: fileIndex > 0 ? () => setSelected(rows[fileIndex - 1]?.path ?? "") : undefined,
    next: rows[fileIndex + 1] || files.hasNextPage ? () => { void nextFile(); } : undefined,
    loading: files.isFetchingNextPage,
  };
  return <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] gap-3 md:grid-cols-[16rem_1fr] md:grid-rows-1">
    <nav className="max-h-36 overflow-auto rounded border md:max-h-none" aria-label={t(($) => $.local_review.files)}>
      {rows.map((entry) => <button key={entry.path} type="button" aria-pressed={file?.path === entry.path} onClick={() => setSelected(entry.path)} className={`block w-full break-all px-3 py-2 text-left text-caption hover:bg-accent ${file?.path === entry.path ? "bg-accent font-semibold" : ""}`}>
        <span>{entry.path}</span>
        <span className="mt-1 block text-muted-foreground">{t(($) => $.local_review.file_changes, { additions: entry.additions, deletions: entry.deletions })}</span>
      </button>)}
      <p className="p-2 text-caption text-muted-foreground">{t(($) => $.local_review.loaded_files, { loaded: rows.length, total: manifest.page.total_files })}</p>
      {files.error && <LocalReviewError error={files.error} id={errorId} />}
      {files.hasNextPage && <Button className="m-2" variant="outline" disabled={files.isFetchingNextPage} onClick={() => void files.fetchNextPage()}>{t(($) => $.local_review.load_more_files)}</Button>}
    </nav>
    {file ? <LocalReviewPatch key={manifest.version_id + file.path} request={request} manifest={manifest} filePath={file.path} navigation={navigation} /> : <p className="p-3 text-caption">{t(($) => $.local_review.empty)}</p>}
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
