import { useId, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { Button } from "@multica/ui/components/ui/button";
import { readReviewContext } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";

type ContextProps = {
  readonly request: LocalReviewRequest;
  readonly versionId: string;
  readonly filePath: string;
  readonly oldStart: number;
  readonly newStart: number;
  readonly count: number;
};

export function LocalReviewContext(props: ContextProps) {
  const { t } = useT("issues");
  const [expanded, setExpanded] = useState(false);
  if (!expanded) return <Button variant="ghost" className="w-full justify-start bg-muted whitespace-normal" aria-expanded={false} onClick={() => setExpanded(true)}>{t(($) => $.local_review.expand_context, { count: Math.min(50, props.count) })}</Button>;
  return <div><Button variant="ghost" className="w-full justify-start bg-muted" aria-expanded onClick={() => setExpanded(false)}>{t(($) => $.local_review.collapse_context)}</Button><ContextLines {...props} /></div>;
}

function ContextLines({ request, versionId, filePath, oldStart, newStart, count }: ContextProps) {
  const { t } = useT("issues");
  const errorId = useId();
  const end = newStart - 1 + count;
  const query = useInfiniteQuery({
    queryKey: ["review-hunk-context", request.workspace_id, request.task_id, versionId, filePath, newStart, count],
    queryFn: ({ pageParam, signal }) => readReviewContext({ ...request, action: "context", version_id: versionId, file_path: filePath, offset: pageParam, limit: Math.min(50, end - pageParam) }, signal),
    initialPageParam: newStart - 1,
    getNextPageParam: (last) => last.page && last.page.has_more && last.page.next_line < end ? last.page.next_line : undefined,
    retry: false, staleTime: Infinity, gcTime: 0, networkMode: "always", refetchOnWindowFocus: false,
  });
  const next = query.data?.pages.at(-1)?.page?.next_line ?? newStart - 1;
  return <div>
    {query.isPending && <p role="status" className="p-2 whitespace-normal">{t(($) => $.local_review.loading)}</p>}
    {query.error && <div className="p-2 whitespace-normal"><LocalReviewError error={query.error} id={errorId} /><Button variant="outline" onClick={() => void query.refetch()}>{t(($) => $.local_review.refresh)}</Button></div>}
    {query.data?.pages.flatMap((page) => page.page?.lines ?? []).map((line) => <div key={line.new_line}><span className="mr-2 inline-block w-10 select-none text-right text-muted-foreground">{oldStart + (line.new_line ?? newStart) - newStart}</span><span className="mr-3 inline-block w-10 select-none text-right text-muted-foreground">{line.new_line}</span>{line.text || " "}</div>)}
    {query.hasNextPage && <Button variant="ghost" className="w-full justify-start bg-muted whitespace-normal" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{t(($) => $.local_review.expand_context, { count: Math.min(50, end - next) })}</Button>}
  </div>;
}
