"use client";

import { useCallback, useEffect, useId, useRef, useState, type RefObject } from "react";
import type { DocumentComment } from "@multica/core/chat";
import { annotationRange } from "../../issues/components/comment-annotation-selection";

function sourceRange(container: HTMLElement, comment: DocumentComment): Range | null {
  const root = container.querySelector<HTMLElement>("[data-document-readable]");
  const anchor = comment.anchor;
  if (!root || !anchor) return null;
  return annotationRange(root, { id: comment.id, sourceCommentId: "document-body", sourceActorName: "", note: "", ...anchor });
}

export function useDocumentCommentLocations({ container, comments, ready, version }: {
  readonly container: RefObject<HTMLDivElement | null>;
  readonly comments: readonly DocumentComment[];
  readonly ready: boolean;
  readonly version: string | number | undefined;
}) {
  const [locations, setLocations] = useState<Readonly<Record<string, boolean>>>({});
  const selected = useRef<string | null>(null);
  const highlightName = `document-comment-${useId().replace(/[^a-zA-Z0-9-]/g, "")}`;
  useEffect(() => {
    const element = container.current;
    const root = element?.querySelector<HTMLElement>("[data-document-readable]");
    if (!ready || !element || !root) { setLocations({}); return; }
    const update = () => {
      const next = Object.fromEntries(comments.filter((comment) => comment.anchor).map((comment) => [comment.id, !!sourceRange(element, comment)]));
      setLocations((previous) => Object.keys(previous).length === Object.keys(next).length && Object.entries(next).every(([key, value]) => previous[key] === value) ? previous : next);
      const active = comments.find((comment) => comment.id === selected.current);
      const range = active && sourceRange(element, active);
      if (typeof Highlight !== "undefined" && typeof CSS !== "undefined" && CSS.highlights) {
        if (range) CSS.highlights.set(highlightName, new Highlight(range));
        else CSS.highlights.delete(highlightName);
      }
    };
    update();
    const observer = new MutationObserver(update);
    observer.observe(root, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [comments, container, highlightName, ready, version]);
  useEffect(() => () => {
    if (typeof CSS !== "undefined" && CSS.highlights) CSS.highlights.delete(highlightName);
  }, [highlightName]);
  const locate = useCallback((comment: DocumentComment) => {
    const element = container.current;
    if (!ready || !element) return false;
    const range = sourceRange(element, comment);
    if (!range) return false;
    const parent = range.startContainer instanceof Element ? range.startContainer : range.startContainer.parentElement;
    parent?.scrollIntoView({ block: "center", behavior: "instant" });
    selected.current = comment.id;
    if (typeof Highlight !== "undefined" && typeof CSS !== "undefined" && CSS.highlights) CSS.highlights.set(highlightName, new Highlight(range));
    return true;
  }, [container, highlightName, ready]);
  return { locations, locate, highlightName };
}
