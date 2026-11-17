import { useEffect, useRef } from "react";
import type { DocumentTextLocation } from "@multica/core/chat";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function WorkspaceSearchSource({ content, location, onPreview }: {
  readonly content: string;
  readonly location: DocumentTextLocation;
  readonly onPreview: () => void;
}) {
  const { t } = useT("chat");
  const marker = useRef<HTMLElement>(null);
  const located = location.text.length > 0 && location.offset >= 0 && content.slice(location.offset, location.offset + location.text.length) === location.text;
  useEffect(() => {
    if (located) marker.current?.scrollIntoView({ block: "center", behavior: "instant" });
  }, [located, location]);
  return <>
    <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-caption text-muted-foreground">
      <span>{t(($) => $.doc_mode.source)} · {t(($) => $.doc_mode.cursor_position, { line: location.line, column: location.offset - content.lastIndexOf("\n", location.offset - 1) })}</span>
      <Button type="button" variant="outline" size="xs" onClick={onPreview}>{t(($) => $.doc_mode.preview)}</Button>
    </div>
    {!located && <p role="alert" className="mb-3 text-body text-muted-foreground">{t(($) => $.doc_file_list.content_changed)}</p>}
    <pre className="m-0 whitespace-pre-wrap break-words font-mono text-caption">{located ? <>{content.slice(0, location.offset)}<mark ref={marker} className="bg-[Highlight] text-[HighlightText]">{location.text}</mark>{content.slice(location.offset + location.text.length)}</> : content}</pre>
  </>;
}
