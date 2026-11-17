"use client";

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { autoUpdate, computePosition, flip, hide, offset, shift } from "@floating-ui/dom";
import { FilePlus, Loader2, MessageSquareQuote, MessageSquarePlus } from "@multica/ui/icons";
import type { DocumentCommentAnchor } from "@multica/core/chat";
import { Button } from "@multica/ui/components/ui/button";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { toast } from "sonner";
import { useT } from "../../i18n";
import { captureCommentSelection } from "../../issues/components/comment-annotation-selection";

export function useDocSelectionBubble({
  containerRef,
  parentIssueId,
  onAddToChat,
  onAddComment,
}: {
  containerRef: RefObject<HTMLDivElement | null>;
  parentIssueId?: string;
  onAddToChat?: (content: string) => void;
  onAddComment?: (anchor: DocumentCommentAnchor) => void;
}) {
  const { t } = useT("chat");
  const [range, setRange] = useState<Range | null>(null);
  const bubbleRef = useRef<HTMLDivElement>(null);
  const hasActions = !!parentIssueId || !!onAddToChat || !!onAddComment;

  // Read a live, meaningful text selection that lives inside our container.
  const detect = useCallback(() => {
    if (!hasActions) return;
    const container = containerRef.current;
    if (!container) return;
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) {
      setRange(null);
      return;
    }
    const candidate = selection.getRangeAt(0);
    const inside =
      candidate.startContainer instanceof Node &&
      candidate.endContainer instanceof Node &&
      container.contains(candidate.startContainer) &&
      container.contains(candidate.endContainer);
    // A selection outside this container belongs to another surface; leave ours
    // untouched instead of clearing it.
    if (!inside) return;
    if (selection.isCollapsed || !selection.toString().trim()) {
      setRange(null);
      return;
    }
    setRange(candidate);
  }, [containerRef, hasActions]);

  // Re-detect on pointer/keyboard selection anywhere in the document.
  useEffect(() => {
    document.addEventListener("mouseup", detect);
    document.addEventListener("keyup", detect);
    return () => {
      document.removeEventListener("mouseup", detect);
      document.removeEventListener("keyup", detect);
    };
  }, [detect]);

  // Close when the user clicks anywhere outside the toolbar and the content.
  useEffect(() => {
    if (!range) return;
    const handle = (event: MouseEvent) => {
      const target = event.target as Node;
      if (bubbleRef.current?.contains(target)) return;
      if (containerRef.current?.contains(target)) return;
      setRange(null);
    };
    document.addEventListener("mousedown", handle);
    return () => document.removeEventListener("mousedown", handle);
  }, [range, containerRef]);

  // Anchor the portal above the selection; autoUpdate keeps it pinned while the
  // content scrolls (mirrors EditorBubbleMenu / CommentSelectionBubble).
  useEffect(() => {
    if (!range) return;
    const element = bubbleRef.current;
    const container = containerRef.current;
    if (!element || !container) return;
    let active = true;
    const anchor = {
      contextElement: container,
      getBoundingClientRect: () => range.getBoundingClientRect(),
    };
    const update = () => {
      computePosition(anchor, element, {
        strategy: "fixed",
        placement: "top",
        middleware: [
          offset(8),
          flip({ padding: 8, altBoundary: true }),
          shift({ padding: 8, altBoundary: true }),
          hide(),
        ],
      }).then(({ x, y, middlewareData }) => {
        if (!active || !element.isConnected) return;
        const rect = range.getBoundingClientRect();
        const hidden =
          !range.startContainer.isConnected ||
          !container.isConnected ||
          (rect.width === 0 && rect.height === 0) ||
          middlewareData.hide?.referenceHidden;
        element.style.visibility = hidden ? "hidden" : "visible";
        element.style.left = `${x}px`;
        element.style.top = `${y}px`;
      });
    };
    const cleanup = autoUpdate(anchor, element, update);
    update();
    return () => {
      active = false;
      cleanup();
    };
  }, [range, containerRef]);

  // Drop the native selection so a later mouseup cannot re-open the toolbar.
  const clear = useCallback(() => {
    window.getSelection()?.removeAllRanges();
    setRange(null);
  }, []);

  const onAddToConversation = useCallback(() => {
    if (!range) return;
    const text = range.toString();
    onAddToChat?.(text);
    clear();
  }, [range, onAddToChat, clear]);

  if (!range) return { bubble: null };

  return {
    bubble: createPortal(
      <div
        ref={bubbleRef}
        onMouseDown={(e) => e.preventDefault()}
        className="doc-selection-bubble"
        style={{ position: "fixed", zIndex: 50, width: "max-content", visibility: "hidden" }}
      >
        <div className="flex items-center gap-1 rounded-lg border border-border bg-popover p-1 shadow-md">
          {parentIssueId && (
            <DocSubIssueButton range={range} parentIssueId={parentIssueId} clear={clear} />
          )}
          {onAddToChat && (
            <Button size="sm" variant="ghost" className="gap-1.5" onClick={onAddToConversation}>
              <MessageSquareQuote className="h-3.5 w-3.5" />
              {t(($) => $.doc_mode.add_to_chat)}
            </Button>
          )}
          {onAddComment && <Button size="sm" variant="ghost" className="gap-1.5" onClick={() => {
            const container = containerRef.current;
            const captured = container && captureCommentSelection(container, window.getSelection());
            if (!captured) return;
            if (captured.quote.length > 4000) { toast.error(t(($) => $.document_comments.quote_limit)); return; }
            onAddComment({ quote: captured.quote, start: captured.start, prefix: captured.prefix, suffix: captured.suffix });
            clear();
          }}><MessageSquarePlus className="size-3.5" />{t(($) => $.document_comments.add)}</Button>}
        </div>
      </div>,
      document.body,
    ),
  };
}

/**
 * DocSubIssueButton — the "add sub-issue" action of the selection toolbar.
 *
 * Only mounts when a parent issue is in scope, so useCreateIssue() (which
 * resolves the workspace id via useWorkspaceId) is never called for surfaces
 * without a parent — e.g. workspace files — or in a workspace-less test
 * tree. Kept out of useDocSelectionBubble so that hook can run anywhere.
 */
function DocSubIssueButton({
  range,
  parentIssueId,
  clear,
}: {
  range: Range;
  parentIssueId: string;
  clear: () => void;
}) {
  const { t } = useT("chat");
  const createIssue = useCreateIssue();
  const [pending, setPending] = useState(false);
  const handleClick = async () => {
    if (pending) return;
    // Title from the selection: collapse whitespace, cap length (issue-detail's
    // CreateSubIssueButton caps the same way). Read-only, so we never rewrite it.
    const title = range.toString().replace(/\s+/g, " ").trim().slice(0, 200);
    if (!title) return;
    setPending(true);
    try {
      const created = await createIssue.mutateAsync({ title, parent_issue_id: parentIssueId });
      toast.success(t(($) => $.doc_mode.sub_issue_created, { identifier: created.identifier }));
      clear();
    } catch (err) {
      toast.error(
        err instanceof Error && err.message ? err.message : t(($) => $.doc_mode.sub_issue_failed),
      );
    } finally {
      setPending(false);
    }
  };
  return (
    <Button size="sm" variant="ghost" className="gap-1.5" onClick={handleClick} disabled={pending}>
      {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FilePlus className="h-3.5 w-3.5" />}
      {t(($) => $.doc_mode.add_sub_issue)}
    </Button>
  );
}

/** Displays the selected document while the session conversation stays mounted. */
