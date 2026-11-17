import { Loader2 } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import type { useWorkspaceFileSearch } from "./use-workspace-file-search";

export function WorkspaceFileSearchStatus({ search }: { readonly search: ReturnType<typeof useWorkspaceFileSearch> }) {
  const { t } = useT("chat");
  return <>
    {search.searching && <p role="status" className="flex items-center gap-2 px-3 py-2 text-body text-muted-foreground"><Loader2 className="size-4 animate-spin motion-reduce:animate-none" />{t(($) => $.doc_file_list.searching)}</p>}
    {search.isError && <div role="alert" className="px-3 py-2 text-body text-destructive">{t(($) => $.doc_file_list.search_failed)} <Button type="button" variant="ghost" size="sm" onClick={() => void (search.data ? search.fetchNextPage() : search.refetch())}>{t(($) => $.doc_file_list.retry)}</Button></div>}
    {search.paused && !search.isError && <div className="space-y-1 px-3 py-2">
      <p role="status" className="text-caption text-muted-foreground">{t(($) => $.doc_file_list.search_partial)}</p>
      <Button variant="outline" size="sm" disabled={search.isFetching} onClick={search.continueSearch}>{t(($) => $.doc_file_list.continue_search)}</Button>
    </div>}
  </>;
}
