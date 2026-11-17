import { Loader2 } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import type { useWorkspaceContentSearch } from "./use-workspace-content-search";
import type { ContentSearchMatch } from "./workspace-content-search";

type ContentSearch = ReturnType<typeof useWorkspaceContentSearch>;

export function WorkspaceContentSearchStatus({ search }: { readonly search: ContentSearch }) {
  const { t } = useT("chat");
  return <div className="space-y-1 px-1 pt-2 text-caption text-muted-foreground">
    <p>{t(($) => $.doc_file_list.content_scope)}</p>
    <p role="status" className="flex items-center gap-2">{search.isFetching && <Loader2 className="size-3.5 shrink-0 animate-spin motion-reduce:animate-none" />}{t(($) => $.doc_file_list.content_scanned, { count: search.scannedFiles })}</p>
    {search.isError ? <div role="alert">
      <p className="text-destructive">{t(($) => $.doc_file_list.search_failed)}</p>
      <Button type="button" variant="outline" size="sm" onClick={() => void (search.data ? search.fetchNextPage() : search.refetch())}>{t(($) => $.doc_file_list.retry)}</Button>
    </div> : search.hasNextPage && <Button type="button" variant="outline" size="sm" disabled={search.isFetching} onClick={() => void search.fetchNextPage()}>{t(($) => $.doc_file_list.continue_search)}</Button>}
    {!!search.failedFiles.length && <div role="alert">
      <p>{t(($) => $.doc_file_list.content_unreadable, { count: search.failedFiles.length })}</p>
      <details>
        <summary className="cursor-pointer">{t(($) => $.doc_file_list.error_details)}</summary>
        <ul className="break-words font-mono">{search.failedFiles.map((path) => <li key={path}>{path}</li>)}</ul>
      </details>
      <Button type="button" variant="ghost" size="sm" disabled={search.isFetching} onClick={() => void search.restart()}>{t(($) => $.doc_file_list.retry)}</Button>
    </div>}
    {!!search.truncatedFiles.length && <p>{t(($) => $.doc_file_list.content_truncated, { count: search.truncatedFiles.length })}</p>}
  </div>;
}

export function WorkspaceContentSearchResults({ search, onOpen }: {
  readonly search: ContentSearch;
  readonly onOpen: (match: ContentSearchMatch) => void;
}) {
  const { t } = useT("chat");
  if (!search.matches.length) return search.isFetching || search.isPending || search.isError ? null : <p className="p-3 text-body text-muted-foreground">{t(($) => $.doc_file_list.content_no_match)}</p>;
  return <ul aria-label={t(($) => $.doc_file_list.content_results)}>
    {search.matches.map((match) => <li key={`${match.path}:${match.line}`}>
      <button type="button" onClick={() => onOpen(match)} className="block w-full space-y-1 px-3 py-2 text-left hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="block truncate text-caption font-medium" dir="auto">{`${match.path}:${match.line}`}</span>
        <span className="block break-words font-mono text-caption text-muted-foreground" dir="auto">{match.text}</span>
      </button>
    </li>)}
  </ul>;
}
