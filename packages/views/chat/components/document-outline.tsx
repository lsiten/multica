"use client";

import type { RefObject } from "react";
import { useT } from "../../i18n";

export function DocumentOutline({ content, readingContainer }: {
  readonly content: string;
  readonly readingContainer: RefObject<HTMLDivElement | null>;
}) {
  const { t } = useT("chat");
  const headings = [...content.matchAll(/^(#{1,3})\s+(.+?)\s*#*\s*$/gm)]
    .map((match) => ({ level: match[1]?.length ?? 1, title: match[2]?.trim() ?? "" }))
    .filter((heading) => !!heading.title);
  return (
    <nav aria-label={t(($) => $.doc_mode.outline)}>
      <h3 className="mb-3 text-caption font-semibold text-muted-foreground">{t(($) => $.doc_mode.outline)}</h3>
      {headings.length ? <ol className="space-y-1">
        {headings.map((heading, index) => <li key={`${heading.level}:${heading.title}:${index}`} style={{ paddingInlineStart: `${(heading.level - 1) * 0.75}rem` }}>
          <button type="button" className="w-full rounded-sm px-1 py-1 text-left text-caption text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => {
            readingContainer.current?.querySelectorAll("h1,h2,h3").item(index)?.scrollIntoView({ block: "start", behavior: "instant" });
          }}>{heading.title}</button>
        </li>)}
      </ol> : <p className="text-caption text-faint-foreground">{t(($) => $.doc_mode.no_outline)}</p>}
    </nav>
  );
}
