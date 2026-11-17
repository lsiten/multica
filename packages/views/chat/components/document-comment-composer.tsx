"use client";

import { useEffect, useRef, useState } from "react";
import { X, Loader2 } from "@multica/ui/icons";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Button } from "@multica/ui/components/ui/button";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { useCreateDocumentComment, useDocumentCommentDraftStore, documentCommentScopeKey, EMPTY_DOCUMENT_COMMENT_DRAFT, type DocumentRef } from "@multica/core/chat";
import { isImeComposing } from "@multica/core/utils";
import { useT } from "../../i18n";

export function DocumentCommentComposer({ wsId, resource, parentId, onSubmitted, autoFocus = false }: {
  readonly wsId: string;
  readonly resource: DocumentRef;
  readonly parentId?: string;
  readonly onSubmitted?: () => void;
  readonly autoFocus?: boolean;
}) {
  const { t } = useT("chat");
  const scope = documentCommentScopeKey(wsId, resource, parentId);
  const draft = useDocumentCommentDraftStore((state) => state.drafts[scope] ?? EMPTY_DOCUMENT_COMMENT_DRAFT);
  const post = useCreateDocumentComment(wsId, resource);
  const [failed, setFailed] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (autoFocus && draft.anchor) input.current?.focus();
  }, [autoFocus, draft.anchor]);
  const submit = async () => {
    if (post.isPending || !draft.content.trim()) return;
    const sent = draft;
    setFailed(false);
    try {
      await post.mutateAsync({ submission_id: sent.submissionId, content: sent.content, parent_id: parentId, anchor: parentId ? undefined : sent.anchor });
      const unchanged = useDocumentCommentDraftStore.getState().drafts[scope] === sent;
      useDocumentCommentDraftStore.getState().clearDraft(scope, sent);
      if (unchanged) onSubmitted?.();
    } catch (error) {
      if (!(error instanceof Error)) throw error;
      setFailed(true);
    }
  };
  return (
    <div className="space-y-2">
      {!parentId && draft.anchor && <div className="flex items-start gap-1 rounded-md bg-muted/50 p-2">
        <blockquote className="max-h-24 min-w-0 flex-1 overflow-y-auto whitespace-pre-wrap break-words text-caption text-muted-foreground">{draft.anchor.quote}</blockquote>
        <IconButton size="icon-xs" label={t(($) => $.document_comments.remove_quote)} onClick={() => useDocumentCommentDraftStore.getState().setDraft(scope, { content: draft.content })}><X /></IconButton>
      </div>}
      <Textarea ref={input} value={draft.content} maxLength={16000} autoFocus={autoFocus} className="max-h-48 min-h-20 resize-y text-body" aria-label={t(($) => parentId ? $.document_comments.reply_placeholder : $.document_comments.note_placeholder)} placeholder={t(($) => parentId ? $.document_comments.reply_placeholder : $.document_comments.note_placeholder)} onChange={(event) => useDocumentCommentDraftStore.getState().setDraft(scope, { content: event.target.value, anchor: draft.anchor })} onKeyDown={(event) => {
        if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !isImeComposing(event)) {
          event.preventDefault();
          void submit();
        }
      }} />
      {failed && <p role="alert" className="text-caption text-destructive">{t(($) => $.document_comments.post_failed)}</p>}
      <div className="flex justify-end">
        <Button size="sm" disabled={!draft.content.trim() || post.isPending} onClick={() => void submit()}>
          {post.isPending && <Loader2 className="size-3.5 animate-spin" />}
          {t(($) => post.isPending ? $.document_comments.posting : parentId ? $.document_comments.submit_reply : $.document_comments.submit)}
        </Button>
      </div>
    </div>
  );
}
