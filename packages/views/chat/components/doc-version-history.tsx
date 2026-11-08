"use client";

import { useState } from "react";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { Button } from "@multica/ui/components/ui/button";
import { Loader2 } from "@multica/ui/icons";
import { diffTraceLines } from "../../common/task-transcript/trace-event-presenter";
import { renderDiffRows } from "../../common/task-transcript/detail-surfaces";

export function DocVersionHistory({
  versions,
  current,
  loading,
  onRollback,
  rollbackLabel,
  diffLabel,
}: {
  readonly versions: DocumentContentVersion[];
  readonly current: DocumentContentVersion | undefined;
  readonly loading: boolean;
  readonly onRollback: (revision: number) => void;
  readonly rollbackLabel: string;
  readonly diffLabel: string;
}) {
  const [selectedRevision, setSelectedRevision] = useState<number | null>(null);
  const selected = versions.find((version) => version.revision === selectedRevision)
    ?? versions.find((version) => version.revision !== current?.revision)
    ?? versions[0];

  if (loading) {
    return <div className="flex h-full items-center justify-center"><Loader2 className="h-5 w-5 animate-spin" /></div>;
  }
  if (!selected || !current) {
    return <div className="flex h-full items-center justify-center text-muted-foreground">—</div>;
  }

  return (
    <div className="flex min-h-0 flex-col gap-4 p-4">
      <div className="flex flex-wrap items-center gap-2" role="group" aria-label={diffLabel}>
        {versions.map((version) => (
          <Button
            key={version.id}
            size="sm"
            variant={selected.revision === version.revision ? "secondary" : "ghost"}
            aria-pressed={selected.revision === version.revision}
            onClick={() => setSelectedRevision(version.revision)}
          >
            {`v${version.revision}`}
          </Button>
        ))}
        <Button size="sm" variant="ghost" onClick={() => onRollback(selected.revision)}>
          {rollbackLabel}
        </Button>
      </div>
      {selected.note && <p className="text-body text-muted-foreground">{selected.note}</p>}
      <p className="text-caption text-muted-foreground">{`v${selected.revision} → v${current.revision}`}</p>
      {selected.content !== null && current.content !== null && (
        <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono text-caption" aria-label={diffLabel}>
          {renderDiffRows(diffTraceLines(selected.content.split("\n"), current.content.split("\n")), null)}
        </pre>
      )}
    </div>
  );
}
