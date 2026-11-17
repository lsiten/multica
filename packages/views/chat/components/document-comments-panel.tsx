"use client";

import { useId, useMemo, useState, type RefObject } from "react";
import { Loader2, X } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { useDocumentComments, useResolveDocumentComment, type DocumentRef, type DocumentComment } from "@multica/core/chat";
import { RichContent } from "../../rich-content";
import { useT } from "../../i18n";
import { DocumentCommentComposer } from "./document-comment-composer";
import { useDocumentCommentLocations } from "./use-document-comment-locations";

export function DocumentCommentsPanel({ wsId, resource, actorLabels, sourceContainer, sourceReady, sourceVersion, onClose }: {
  readonly wsId: string;
  readonly resource: DocumentRef;
  readonly actorLabels: Readonly<Record<string, string>>;
  readonly sourceContainer: RefObject<HTMLDivElement | null>;
  readonly sourceReady: boolean;
  readonly sourceVersion: string | number | undefined;
  readonly onClose: () => void;
}) {
  const { t } = useT("chat");
  const query = useDocumentComments(wsId, resource, true);
  const resolution = useResolveDocumentComment(wsId, resource);
  const [showResolved, setShowResolved] = useState(false);
  const resolvedId = useId();
  const [replyTo, setReplyTo] = useState<string | null>(null);
  const [error, setError] = useState<"action" | "location" | null>(null);
  const { locations, locate, highlightName } = useDocumentCommentLocations({ container: sourceContainer, comments: query.comments, ready: sourceReady, version: sourceVersion });
  const roots = query.comments.filter((comment) => !comment.parent_id && (showResolved || !comment.resolved_at));
  const replies = useMemo(() => {
    const map = new Map<string, DocumentComment[]>();
    for (const comment of query.comments) if (comment.parent_id) map.set(comment.parent_id, [...(map.get(comment.parent_id) ?? []), comment]);
    return map;
  }, [query.comments]);
  const toggleResolution = async (comment: DocumentComment) => {
    setError(null);
    try { await resolution.mutateAsync({ comment, resolved: !comment.resolved_at }); }
    catch (error) { if (!(error instanceof Error)) throw error; setError("action"); }
  };
  return (
    <section className="flex h-full min-h-0 flex-col" aria-label={t(($) => $.document_comments.title)}>
      <style>{`::highlight(${highlightName}) { background: color-mix(in srgb, var(--brand) 20%, transparent); }`}</style>
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-2">
        <h3 className="text-caption font-semibold">{t(($) => $.document_comments.title)}</h3>
        <IconButton size="icon-xs" label={t(($) => $.document_comments.close)} onClick={onClose}><X /></IconButton>
      </div>
      <label htmlFor={resolvedId} className="flex shrink-0 items-center gap-2 px-3 py-2 text-caption"><Checkbox id={resolvedId} checked={showResolved} onCheckedChange={setShowResolved} />{t(($) => $.document_comments.show_resolved)}</label>
      {error && <p role="alert" className="px-3 text-caption text-destructive">{t(($) => error === "location" ? $.document_comments.cannot_locate : $.document_comments.action_failed)}</p>}
      <div className="min-h-0 flex-1 overflow-y-auto" aria-busy={query.isFetching}>
        {query.isPending && <div role="status" className="flex justify-center p-4"><Loader2 className="size-4 animate-spin" aria-label={t(($) => $.list.loading)} /></div>}
        {query.isError && <div className="space-y-2 p-3"><p role="alert" className="text-caption text-destructive">{t(($) => $.document_comments.load_failed)}</p><Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.doc_mode.retry)}</Button></div>}
        {!query.isPending && !query.isError && roots.length === 0 && <p className="p-3 text-caption text-muted-foreground">{t(($) => $.document_comments.empty)}</p>}
        {roots.map((comment) => <article key={comment.id} className="space-y-2 border-b p-3">
          <div className="flex items-center justify-between gap-2 text-caption">
            <span className="truncate font-medium">{actorLabels[comment.author_id] ?? t(($) => $.document_comments.actor)}</span>
            {comment.resolved_at && <span className="text-muted-foreground">{t(($) => $.document_comments.resolved)}</span>}
          </div>
          {comment.anchor && <div className="space-y-1 border-l-2 border-brand/40 pl-2">
            <blockquote className="max-h-24 overflow-y-auto whitespace-pre-wrap break-words text-caption text-muted-foreground">{comment.anchor.quote}</blockquote>
            {locations[comment.id] === false ? <p className="text-caption text-warning">{t(($) => $.document_comments.stale)}</p>
              : <Button size="xs" variant="ghost" disabled={!sourceReady} onClick={() => { if (!locate(comment)) setError("location"); }}>{t(($) => $.document_comments.locate)}</Button>}
          </div>}
          <RichContent content={comment.content} density="compact" phase="settled" />
          <div className="flex flex-wrap gap-1">
            <Button size="xs" variant="ghost" onClick={() => setReplyTo(replyTo === comment.id ? null : comment.id)}>{t(($) => replyTo === comment.id ? $.document_comments.close_reply : $.document_comments.reply)}</Button>
            <Button size="xs" variant="ghost" disabled={resolution.isPending} aria-busy={resolution.isPending && resolution.variables?.comment.id === comment.id} onClick={() => void toggleResolution(comment)}>{resolution.isPending && resolution.variables?.comment.id === comment.id && <Loader2 className="size-3 animate-spin" />}{t(($) => comment.resolved_at ? $.document_comments.reopen : $.document_comments.resolve)}</Button>
          </div>
          {(replies.get(comment.id) ?? []).map((reply) => <div key={reply.id} className="space-y-1 border-l pl-3">
            <p className="text-caption font-medium">{actorLabels[reply.author_id] ?? t(($) => $.document_comments.actor)}</p>
            <RichContent content={reply.content} density="compact" phase="settled" />
          </div>)}
          {replyTo === comment.id && <DocumentCommentComposer wsId={wsId} resource={resource} parentId={comment.id} autoFocus onSubmitted={() => setReplyTo(null)} />}
        </article>)}
        {query.hasNextPage && <div className="p-3"><Button size="sm" variant="outline" disabled={query.isFetching} onClick={() => void query.fetchNextPage()}>{t(($) => $.document_comments.load_more)}</Button></div>}
      </div>
      {!query.isPending && !query.isError && <div className="shrink-0 space-y-2 border-t p-3">
        <h4 className="text-caption font-medium">{t(($) => $.document_comments.new_note)}</h4>
        <DocumentCommentComposer wsId={wsId} resource={resource} autoFocus />
      </div>}
    </section>
  );
}
