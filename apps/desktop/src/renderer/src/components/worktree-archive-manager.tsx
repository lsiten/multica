import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "@multica/views/i18n";
import { invalidateWorktreeInventory } from "@multica/views/platform";
import type { ManagedWorktree } from "@multica/core/types/managed-worktree";
import type { WorktreeArchiveResult, WorktreeArchiveSummary } from "@multica/core/types/worktree-archives";
import type { WorktreeFilters } from "@multica/core/types/worktree-filters";

function archiveOperationIdentity(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function WorktreeArchiveManager({ rows, enabled, disabled, queryKey, filters, onPendingChange }: {
  rows: readonly ManagedWorktree[];
  enabled: boolean;
  disabled: boolean;
  queryKey: readonly unknown[];
  filters: WorktreeFilters;
  onPendingChange: (pending: boolean) => void;
}) {
  const { t } = useT("settings");
  const client = useQueryClient();
  const workspaceId = filters.workspace || undefined;
  const [open, setOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [receipts, setReceipts] = useState<WorktreeArchiveResult[]>([]);
  const [restoreTarget, setRestoreTarget] = useState<WorktreeArchiveSummary | null>(null);
  const scan = useMutation({
    mutationFn: () => window.daemonAPI.environmentArchiveOperation({ action: "preview", workspaceId }),
    onSuccess: () => { setReceipts([]); setOpen(true); },
  });
  const history = useQuery({
    queryKey,
    queryFn: () => window.daemonAPI.listEnvironmentArchives(workspaceId),
    enabled: enabled && historyOpen,
    retry: false,
  });
  const preview = (scan.data ?? []).filter((result) => rows.some((row) => row.environmentId === result.environmentId));
  const eligible = preview.filter((result) => !result.reason && result.revision);
  const archive = useMutation({
    mutationFn: async () => {
      const results: WorktreeArchiveResult[] = [];
      const operationId = archiveOperationIdentity();
      for (let offset = 0; offset < eligible.length; offset += 1000) {
        results.push(...await window.daemonAPI.environmentArchiveOperation({
          action: "archive", workspaceId, operationId,
          selections: eligible.slice(offset, offset + 1000).map(({ environmentId, revision }) => ({ environmentId, revision })),
        }));
        setReceipts([...results]);
      }
    },
    onSuccess: () => setOpen(false),
    onSettled: () => { void invalidateWorktreeInventory(client); void client.invalidateQueries({ queryKey }); },
  });
  const restore = useMutation({
    mutationFn: (target: WorktreeArchiveSummary) => window.daemonAPI.environmentArchiveOperation({ action: "restore", workspaceId: target.workspaceId, archiveId: target.archiveId }),
    onSuccess: (results) => { setReceipts(results); setRestoreTarget(null); },
    onSettled: () => { void invalidateWorktreeInventory(client); void client.invalidateQueries({ queryKey }); },
  });
  const pending = scan.isPending || archive.isPending || restore.isPending;
  useEffect(() => {
    onPendingChange(pending);
    return () => onPendingChange(false);
  }, [pending, onPendingChange]);
  const formatBytes = (bytes: number) => `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
  const reasonLabel = (reason: string) => {
    switch (reason) {
      case "active": return t(($) => $.desktop.worktrees.active);
      case "review": return t(($) => $.desktop.worktrees.review_active);
      case "unowned": return t(($) => $.desktop.worktrees.unowned);
      case "preview_changed": return t(($) => $.desktop.worktrees.archive_changed);
      case "existing_environment": return t(($) => $.desktop.worktrees.restore_existing);
      case "repository_busy": return t(($) => $.desktop.worktrees.repository_busy);
      case "reclaim_failed": return t(($) => $.desktop.worktrees.archive_reclaim_failed);
      case "restore_failed": return t(($) => $.desktop.worktrees.archive_restore_failed);
      case "archive_failed": case "archive_unavailable": case "review_archive_failed": return t(($) => $.desktop.worktrees.archive_capture_failed);
      default: return t(($) => $.desktop.worktrees.unavailable);
    }
  };
  const environmentName = (id: string, taskId: string) => rows.find((row) => row.environmentId === id)?.taskName ?? taskId;
  const filteredHistory = (history.data ?? []).filter((row) => {
    if (filters.project && row.projectId !== filters.project) return false;
    if (filters.squad && row.squadId !== filters.squad) return false;
    if (filters.kind && row.kind !== filters.kind) return false;
    const search = filters.search.trim().toLocaleLowerCase();
    return !search || [row.taskName, row.taskId, row.agentName, row.originalPath, row.projectName, row.squadName].some((value) => value.toLocaleLowerCase().includes(search));
  });
  return <div className="space-y-3 px-4 py-3">
    <Button variant="outline" size="sm" disabled={disabled || pending || !rows.some((row) => row.environmentId)} aria-busy={pending} onClick={() => { archive.reset(); scan.mutate(); }}>
      {scan.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Archive className="size-3.5" />}{t(($) => $.desktop.worktrees.scan_archive)}
    </Button>
    {(scan.isError || archive.isError || restore.isError) && <p role="alert" className="break-words text-body text-destructive">{(scan.error ?? archive.error ?? restore.error)?.message}</p>}
    {receipts.length > 0 && <div role="status" className="space-y-2 text-caption">
      {receipts.map((result) => <p key={result.environmentId || result.archiveId} className="break-words">
        {environmentName(result.environmentId, result.taskId)} · {result.reclaimed ? t(($) => $.desktop.worktrees.archive_reclaimed, { size: formatBytes(result.archiveBytes) }) : result.restored ? t(($) => $.desktop.worktrees.archive_restored) : reasonLabel(result.reason)}
      </p>)}
    </div>}
    <details open={historyOpen} onToggle={(event) => setHistoryOpen(event.currentTarget.open)}>
      <summary className="cursor-pointer text-body font-medium">{t(($) => $.desktop.worktrees.archive_history)}</summary>
      {history.isFetching && <p className="py-2 text-caption">{t(($) => $.desktop.worktrees.archive_loading)}</p>}
      {history.isError && <p role="alert" className="py-2 text-body text-destructive">{history.error.message}</p>}
      {history.isSuccess && filteredHistory.length === 0 && <p className="py-2 text-caption text-muted-foreground">{t(($) => $.desktop.worktrees.archive_empty)}</p>}
      <div className="max-h-80 space-y-3 overflow-y-auto py-3">
        {filteredHistory.map((row) => <div key={row.archiveId} className="flex items-start gap-3 text-caption">
          <div className="min-w-0 flex-1">
            <p className="break-words font-medium">{row.taskName} · {row.agentName || row.taskId}</p>
            <p className="break-all font-mono text-muted-foreground">{row.originalPath}</p>
            <p className="text-muted-foreground"><time dateTime={row.createdAt}>{new Date(row.createdAt).toLocaleString()}</time> · {formatBytes(row.archiveBytes)}</p>
            {row.restoreReason && <p className="text-muted-foreground">{reasonLabel(row.restoreReason)}</p>}
          </div>
          <Button variant="outline" size="sm" disabled={disabled || !enabled || pending || !!row.restoreReason} onClick={() => { restore.reset(); setRestoreTarget(row); }}>{t(($) => $.desktop.worktrees.restore_archive)}</Button>
        </div>)}
      </div>
    </details>
    <Dialog open={open} onOpenChange={(value) => { if (!archive.isPending) setOpen(value); }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(($) => $.desktop.worktrees.archive_preview)}</DialogTitle><DialogDescription>{t(($) => $.desktop.worktrees.archive_preserves)}</DialogDescription></DialogHeader>
        <p className="text-body">{t(($) => $.desktop.worktrees.archive_estimate, { count: eligible.length, size: formatBytes(eligible.reduce((sum, row) => sum + row.originalBytes, 0)) })}</p>
        <p className="text-caption text-muted-foreground">{t(($) => $.desktop.worktrees.archive_disk_required)}</p>
        {eligible.length === 0 && <p className="text-caption">{t(($) => $.desktop.worktrees.archive_none_eligible)}</p>}
        <div className="max-h-64 space-y-2 overflow-y-auto text-caption">{preview.map((row) => <p className="break-words" key={row.environmentId}>{environmentName(row.environmentId, row.taskId)} · {row.reason ? reasonLabel(row.reason) : formatBytes(row.originalBytes)}</p>)}</div>
        <DialogFooter>
          <Button variant="ghost" disabled={archive.isPending} onClick={() => setOpen(false)}>{t(($) => $.desktop.daemon.cancel)}</Button>
          <Button disabled={archive.isPending || eligible.length === 0 || archive.isError} aria-busy={archive.isPending} onClick={() => archive.mutate()}>{archive.isPending && <Loader2 className="size-3.5 animate-spin" />}{t(($) => $.desktop.worktrees.archive_reclaim)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <Dialog open={restoreTarget !== null} onOpenChange={(value) => { if (!value && !restore.isPending) setRestoreTarget(null); }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(($) => $.desktop.worktrees.restore_archive)}</DialogTitle><DialogDescription>{t(($) => $.desktop.worktrees.restore_preserves)}</DialogDescription></DialogHeader>
        <p className="break-all font-mono text-caption">{restoreTarget?.originalPath}</p>
        <DialogFooter>
          <Button variant="ghost" disabled={restore.isPending} onClick={() => setRestoreTarget(null)}>{t(($) => $.desktop.daemon.cancel)}</Button>
          <Button disabled={restore.isPending || restore.isError} aria-busy={restore.isPending} onClick={() => { if (restoreTarget) restore.mutate(restoreTarget); }}>{restore.isPending && <Loader2 className="size-3.5 animate-spin" />}{t(($) => $.desktop.worktrees.restore_archive)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}
