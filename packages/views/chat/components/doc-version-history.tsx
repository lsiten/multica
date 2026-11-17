"use client";

import { useMemo, useState } from "react";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { Button } from "@multica/ui/components/ui/button";
import { Loader2, Trash2 } from "@multica/ui/icons";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { diffTraceLines } from "../../common/task-transcript/trace-event-presenter";
import { renderDiffRows } from "../../common/task-transcript/detail-surfaces";
import { useLocale, useT } from "../../i18n";

export function DocVersionHistory({
  versions,
  current,
  actorLabels = {},
  loading,
  onRollback,
  onDelete,
  rollbackLabel,
  diffLabel,
  rollbackConfirmTitle,
  rollbackConfirmDescription,
  cancelLabel,
  onAddToChat,
}: {
  readonly versions: DocumentContentVersion[];
  readonly current: DocumentContentVersion | undefined;
  readonly actorLabels?: Readonly<Record<string, string>>;
  readonly loading: boolean;
  readonly onRollback: (revision: number) => Promise<unknown>;
  readonly onDelete: (revision: number) => Promise<void>;
  readonly rollbackLabel: string;
  readonly diffLabel: string;
  readonly rollbackConfirmTitle: string;
  readonly rollbackConfirmDescription: string;
  readonly cancelLabel: string;
  readonly onAddToChat?: (content: string, revision: number) => void;
}) {
  const { t } = useT("chat");
  const locale = useLocale();
  const [selectedRevision, setSelectedRevision] = useState<number | null>(null);
  const [confirmRollback, setConfirmRollback] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [restoreError, setRestoreError] = useState(false);
  const [deleteRevision, setDeleteRevision] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState(false);
  const [mode, setMode] = useState<"preview" | "diff">("preview");
  const changesByRevision = useMemo(() => {
    const counts = new Map<number, { additions: number; deletions: number }>();
    const chronological = [...versions].sort(
      (first, second) => first.revision - second.revision,
    );
    for (let index = 1; index < chronological.length; index++) {
      const previous = chronological[index - 1];
      const version = chronological[index];
      if (!previous || !version || previous.content === null || version.content === null) {
        continue;
      }
      const diff = diffTraceLines(
        previous.content.split("\n"),
        version.content.split("\n"),
      );
      counts.set(version.revision, {
        additions: diff.filter((line) => line.kind === "add").length,
        deletions: diff.filter((line) => line.kind === "remove").length,
      });
    }
    return counts;
  }, [versions]);
  const selected = versions.find((version) => version.revision === selectedRevision)
    ?? versions.find((version) => version.revision !== current?.revision)
    ?? versions[0];

  if (loading) {
    return <div className="flex h-full items-center justify-center"><Loader2 className="h-5 w-5 animate-spin" /></div>;
  }
  if (!selected || !current) {
    return <div className="flex h-full items-center justify-center p-4 text-body text-muted-foreground">{t(($) => $.doc_mode.history_empty)}</div>;
  }
  const groups = groupVersions(versions, locale);
  const impact = selected.content !== null && current.content !== null
    ? diffTraceLines(current.content.split("\n"), selected.content.split("\n"))
        .reduce(
          (counts, line) => ({
            additions: counts.additions + Number(line.kind === "add"),
            deletions: counts.deletions + Number(line.kind === "remove"),
          }),
          { additions: 0, deletions: 0 },
        )
    : null;
  const versionCard = (version: DocumentContentVersion) => {
    const active = selected.revision === version.revision;
    const currentVersion = version.revision === current.revision;
    const changes = changesByRevision.get(version.revision);
    return (
      <li key={`${version.id}:${version.revision}`}>
        <div className={`group flex items-start rounded-md border transition-colors ${active ? "border-primary/40 bg-accent" : "border-transparent hover:bg-accent/60"}`}>
          <button
            type="button"
            aria-current={active ? "true" : undefined}
            className="min-w-0 flex-1 px-3 py-2 text-left"
            onClick={() => {
              setSelectedRevision(version.revision);
              setMode("preview");
            }}
          >
            <span className="flex items-center justify-between gap-2">
              <span className="text-caption font-medium">
                {`v${version.revision}`}
                {currentVersion ? ` · ${t(($) => $.doc_mode.current_version)}` : ""}
              </span>
              <time
                dateTime={version.created_at}
                className="shrink-0 text-caption text-muted-foreground"
              >
                {new Date(version.created_at).toLocaleTimeString(locale, {
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </time>
            </span>
            <span className="mt-0.5 block text-caption text-muted-foreground">
              {actorLabels[version.actor_id] ?? (version.actor_type === "agent"
                ? t(($) => $.doc_mode.agent_author)
                : t(($) => $.doc_mode.member_author))}
              <span title={version.actor_id}> · {version.actor_id.slice(0, 8)}</span>
            </span>
            {version.note && (
              <span className="mt-1 line-clamp-2 block break-words text-caption text-muted-foreground">
                {version.note}
              </span>
            )}
            {changes && (
              <span
                className="mt-1 flex items-center gap-2 tabular-nums text-caption text-muted-foreground"
                aria-label={t(($) => $.doc_mode.history_changes, changes)}
              >
                <span className="text-success">+{changes.additions}</span>
                <span className="text-destructive">−{changes.deletions}</span>
              </span>
            )}
          </button>
          {!version.merged && !currentVersion && (
            <Button
              size="icon-xs"
              variant="ghost"
              className="mr-1 mt-1 opacity-0 focus:opacity-100 group-hover:opacity-100"
              aria-label={t(($) => $.doc_mode.delete_version, {
                revision: version.revision,
              })}
              onClick={() => {
                setDeleteRevision(version.revision);
                setDeleteError(false);
              }}
            >
              <Trash2 aria-hidden="true" />
            </Button>
          )}
        </div>
      </li>
    );
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        <div className="space-y-4" aria-label={t(($) => $.doc_mode.history)}>
          {groups.map((group) => (
            <section key={group.day} aria-label={group.label}>
              <h3 className="mb-2 px-1 text-caption font-semibold text-muted-foreground">
                {group.label}
              </h3>
              <div className="space-y-3">
                {group.sessions.map((session) => (
                  <div key={session.key}>
                    <h4 className="mb-1 px-1 text-caption text-muted-foreground">
                      {session.sessionId
                        ? t(($) => $.doc_mode.history_session, {
                            id: session.sessionId?.slice(0, 8) ?? "",
                          })
                        : actorLabels[session.actorId] ?? (session.actorType === "agent"
                          ? t(($) => $.doc_mode.agent_author)
                          : t(($) => $.doc_mode.member_author))}
                    </h4>
                    {!!session.checkpoints.length && (
                      <ol className="space-y-1">
                        {session.checkpoints.map(versionCard)}
                      </ol>
                    )}
                    {!!session.autosaves.length && (
                      <details className="mt-2 rounded-md border border-dashed">
                        <summary className="cursor-pointer px-3 py-2 text-caption text-muted-foreground">
                          {t(($) => $.doc_mode.autosave_group, {
                            count: session.autosaves.length,
                          })}
                        </summary>
                        <ol className="space-y-1 border-t p-1">
                          {session.autosaves.map(versionCard)}
                        </ol>
                      </details>
                    )}
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      </div>
      <div className="min-h-0 flex-[2] overflow-y-auto border-t p-3">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-1" role="group" aria-label={diffLabel}>
            <Button
              size="xs"
              variant={mode === "preview" ? "secondary" : "ghost"}
              aria-pressed={mode === "preview"}
              onClick={() => setMode("preview")}
            >
              {t(($) => $.doc_mode.preview)}
            </Button>
            <Button
              size="xs"
              variant={mode === "diff" ? "secondary" : "ghost"}
              aria-pressed={mode === "diff"}
              onClick={() => setMode("diff")}
            >
              {diffLabel}
            </Button>
          </div>
          {selected.revision !== current.revision && (
            <Button size="xs" variant="ghost" onClick={() => {
              setRestoreError(false);
              setConfirmRollback(true);
            }}>
              {rollbackLabel}
            </Button>
          )}
        </div>
        <p className="mb-2 text-caption text-muted-foreground">
          {mode === "diff"
            ? `${`v${selected.revision}`} → ${`v${current.revision}`}`
            : `v${selected.revision}`}
        </p>
        {impact && selected.revision !== current.revision && (
          <p className="mb-2 rounded-md bg-muted/50 px-2 py-1 text-caption text-muted-foreground">
            {t(($) => $.doc_mode.restore_impact, {
              additions: impact.additions,
              deletions: impact.deletions,
            })}
          </p>
        )}
        {mode === "preview" ? (
          <pre className="whitespace-pre-wrap break-words font-mono text-caption">
            {selected.content ?? t(($) => $.doc_mode.binary_history)}
          </pre>
        ) : selected.content !== null && current.content !== null ? (
          <pre
            className="overflow-x-auto whitespace-pre-wrap break-words font-mono text-caption"
            aria-label={diffLabel}
          >
            {renderDiffRows(
              diffTraceLines(
                selected.content.split("\n"),
                current.content.split("\n"),
              ),
              null,
            )}
          </pre>
        ) : (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.doc_mode.binary_history)}
          </p>
        )}
        {onAddToChat && selected.content !== null && (
          <Button
            size="sm"
            variant="outline"
            className="mt-3"
            onClick={() => onAddToChat(selected.content ?? "", selected.revision)}
          >
            {t(($) => $.doc_mode.use_version_in_chat)}
          </Button>
        )}
      </div>
      <AlertDialog open={confirmRollback} onOpenChange={(open) => {
        if (!restoring) setConfirmRollback(open);
      }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{rollbackConfirmTitle}</AlertDialogTitle>
            <AlertDialogDescription>{rollbackConfirmDescription}</AlertDialogDescription>
          </AlertDialogHeader>
          {restoreError && (
            <p role="alert" className="text-caption text-destructive">
              {t(($) => $.doc_mode.rollback_failed)}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={restoring}>{cancelLabel}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={restoring}
              aria-busy={restoring}
              onClick={(event) => {
                event.preventDefault();
                setRestoring(true);
                setRestoreError(false);
                void onRollback(selected.revision)
                  .then(() => setConfirmRollback(false))
                  .catch(() => setRestoreError(true))
                  .finally(() => setRestoring(false));
              }}
            >
              {restoring && <Loader2 className="animate-spin" aria-hidden="true" />}
              {rollbackLabel}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog
        open={deleteRevision !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setDeleteRevision(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.doc_mode.delete_version_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.doc_mode.delete_version_description, {
                revision: deleteRevision ?? 0,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && (
            <p role="alert" className="text-caption text-destructive">
              {t(($) => $.doc_mode.delete_version_failed)}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>{cancelLabel}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleting}
              onClick={(event) => {
                event.preventDefault();
                if (deleteRevision === null) return;
                setDeleting(true);
                setDeleteError(false);
                void onDelete(deleteRevision)
                  .then(() => {
                    if (selectedRevision === deleteRevision) setSelectedRevision(null);
                    setDeleteRevision(null);
                  })
                  .catch(() => setDeleteError(true))
                  .finally(() => setDeleting(false));
              }}
            >
              {deleting && <Loader2 className="animate-spin" aria-hidden="true" />}
              {t(($) => $.doc_mode.delete_version_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function groupVersions(
  versions: readonly DocumentContentVersion[],
  locale: string,
): readonly {
  readonly day: string;
  readonly label: string;
  readonly sessions: readonly {
    readonly key: string;
    readonly sessionId: string | null;
    readonly actorId: string;
    readonly actorType: DocumentContentVersion["actor_type"];
    readonly checkpoints: readonly DocumentContentVersion[];
    readonly autosaves: readonly DocumentContentVersion[];
  }[];
}[] {
  const grouped = new Map<
    string,
    {
      label: string;
      sessions: Map<
        string,
        {
          sessionId: string | null;
          actorId: string;
          actorType: DocumentContentVersion["actor_type"];
          checkpoints: DocumentContentVersion[];
          autosaves: DocumentContentVersion[];
        }
      >;
    }
  >();
  for (const version of versions) {
    const date = new Date(version.created_at);
    const day = Number.isNaN(date.valueOf())
      ? version.created_at
      : date.toISOString().slice(0, 10);
    const group = grouped.get(day) ?? {
      label: Number.isNaN(date.valueOf())
        ? version.created_at
        : date.toLocaleDateString(locale, {
            year: "numeric",
            month: "short",
            day: "numeric",
          }),
      sessions: new Map(),
    };
    const sessionKey = version.session_id
      ? `session:${version.session_id}`
      : `actor:${version.actor_type}:${version.actor_id}`;
    const session = group.sessions.get(sessionKey) ?? {
      sessionId: version.session_id ?? null,
      actorId: version.actor_id,
      actorType: version.actor_type,
      checkpoints: [],
      autosaves: [],
    };
    if (version.merged) session.autosaves.push(version);
    else session.checkpoints.push(version);
    group.sessions.set(sessionKey, session);
    grouped.set(day, group);
  }
  return [...grouped.entries()].map(([day, group]) => ({
    day,
    label: group.label,
    sessions: [...group.sessions.entries()].map(([key, session]) => ({
      key,
      ...session,
    })),
  }));
}
