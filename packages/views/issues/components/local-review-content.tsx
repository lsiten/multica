import { useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { Button } from "@multica/ui/components/ui/button";
import { readReviewContent } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";
import type { LocalReviewFileNavigation } from "./local-review-file-browser";

export function LocalReviewContent({ request, manifest, filePath, onBack, navigation }: { request: LocalReviewRequest; manifest: ReviewManifest; filePath: string; onBack: () => void; navigation: LocalReviewFileNavigation }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [side, setSide] = useState<"old" | "new">("new");
  const [offsets, setOffsets] = useState([0]);
  const [index, setIndex] = useState(0);
  const offset = offsets[index] ?? 0;
  const query = useQuery({
    queryKey: ["review-version-content", request.workspace_id, request.task_id, manifest.version_id, filePath, side, offset],
    queryFn: ({ signal }) => readReviewContent({ ...request, action: "content", target: manifest.header.target, version_id: manifest.version_id, file_path: filePath, side, offset, limit: 16384 }, signal),
    retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false, networkMode: "always",
  });
  const content = query.data?.content;
  return <section className="flex min-h-0 flex-col overflow-hidden rounded border">
    <header className="flex shrink-0 flex-wrap items-center gap-2 border-b p-2">
      <p className="min-w-0 flex-1 break-all font-mono text-caption">{filePath}</p>
      <Button variant="ghost" onClick={onBack}>{t(($) => $.local_review.back_to_diff)}</Button>
    </header>
    <div className="flex shrink-0 items-center gap-2 border-b p-2">
      <Button variant={side === "old" ? "secondary" : "ghost"} aria-pressed={side === "old"} onClick={() => { setSide("old"); setOffsets([0]); setIndex(0); }}>{t(($) => $.local_review.content_before)}</Button>
      <Button variant={side === "new" ? "secondary" : "ghost"} aria-pressed={side === "new"} onClick={() => { setSide("new"); setOffsets([0]); setIndex(0); }}>{t(($) => $.local_review.content_after)}</Button>
    </div>
    <div key={side + offset} className="min-h-0 flex-1 overflow-auto p-3">
      {query.isPending && <p role="status" className="text-caption">{t(($) => $.local_review.loading)}</p>}
      {query.error && <><LocalReviewError error={query.error} id={errorId} /><Button variant="outline" className="mt-2" onClick={() => void query.refetch()}>{t(($) => $.local_review.refresh)}</Button></>}
      {content?.encoding === "hex" && <p className="mb-2 text-caption text-muted-foreground">{t(($) => $.local_review.hex_content)}</p>}
      {content && <pre className="whitespace-pre-wrap break-all font-mono text-caption">{content.text || t(($) => $.local_review.empty_content)}</pre>}
    </div>
    <footer className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t p-2">
      <Button variant="outline" disabled={(index === 0 && !navigation.previous) || query.isFetching || navigation.loading} onClick={() => { if (index > 0) setIndex(index - 1); else navigation.previous?.(); }}>{t(($) => $.local_review.previous_patch_page)}</Button>
      {content && <span className="text-caption text-muted-foreground">{t(($) => $.local_review.content_range, { start: content.offset, end: content.next_offset, size: content.size })}</span>}
      <Button variant="outline" disabled={(!content?.has_more && !navigation.next) || query.isFetching || navigation.loading} onClick={() => { if (content?.has_more) { setOffsets([...offsets.slice(0, index + 1), content.next_offset]); setIndex(index + 1); } else navigation.next?.(); }}>{t(($) => $.local_review.next_patch_page)}</Button>
    </footer>
  </section>;
}
