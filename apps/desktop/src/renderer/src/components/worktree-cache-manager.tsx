import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, ScanLine } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "@multica/views/i18n";
import { invalidateWorktreeInventory } from "@multica/views/platform";
import type { ManagedWorktree, WorktreeCacheResult } from "@multica/core/types/managed-worktree";

export function WorktreeCacheManager({ rows, disabled, workspaceId, onPendingChange }: { rows: readonly ManagedWorktree[]; disabled: boolean; workspaceId?: string; onPendingChange?: (pending: boolean) => void }) {
  const { t } = useT("settings");
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [receipts, setReceipts] = useState<WorktreeCacheResult[]>([]);
  const scan = useMutation({
    mutationFn: () => window.daemonAPI.previewWorktreeCaches(workspaceId),
    onSuccess: () => { setReceipts([]); setOpen(true); },
  });
  const preview = (scan.data ?? []).filter((result) => rows.some((row) => row.environmentId === result.environmentId));
  const eligible = preview.filter((result) => result.reason === "" && result.candidates.length > 0 && result.revision !== "");
  const cleanup = useMutation({
    mutationFn: async () => {
      const results: WorktreeCacheResult[] = [];
      for (let offset = 0; offset < eligible.length; offset += 1000) {
        const selections = eligible.slice(offset, offset + 1000).map(({ environmentId, revision }) => ({ environmentId, revision }));
        results.push(...await window.daemonAPI.cleanWorktreeCaches(selections, workspaceId));
        setReceipts([...results]);
      }
    },
    onSuccess: () => setOpen(false),
    onSettled: () => invalidateWorktreeInventory(client),
  });
  const reasonLabel = (reason: string) => {
    switch (reason) {
      case "active": return t(($) => $.desktop.worktrees.active);
      case "review": return t(($) => $.desktop.worktrees.review_active);
      case "unowned": return t(($) => $.desktop.worktrees.unowned);
      case "preview_changed": return t(($) => $.desktop.worktrees.cache_changed);
      case "cancelled": return t(($) => $.desktop.worktrees.cache_cancelled);
      case "removal_failed": return t(($) => $.desktop.worktrees.cache_removal_failed);
      default: return t(($) => $.desktop.worktrees.unavailable);
    }
  };
  const environmentName = (id: string) => rows.find((row) => row.environmentId === id)?.taskName ?? id;
  const formatBytes = (bytes: number) => `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
  const pending = scan.isPending || cleanup.isPending;
  useEffect(() => {
    onPendingChange?.(pending);
    return () => onPendingChange?.(false);
  }, [pending, onPendingChange]);
  return (
    <div className="space-y-3 px-4 py-3">
      <Button variant="outline" size="sm" disabled={disabled || pending || !rows.some((row) => row.environmentId)} aria-busy={pending} onClick={() => { cleanup.reset(); scan.mutate(); }}>
        {scan.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <ScanLine className="size-3.5" />}
        {t(($) => $.desktop.worktrees.scan_cache)}
      </Button>
      {(scan.isError || cleanup.isError) && <p role="alert" className="break-words text-body text-destructive">{(scan.error ?? cleanup.error)?.message}</p>}
      {receipts.length > 0 && <div role="status" className="space-y-2 text-caption">
        <p>{t(($) => $.desktop.worktrees.cache_removed, { count: receipts.reduce((sum, result) => sum + result.removedCount, 0), size: formatBytes(receipts.reduce((sum, result) => sum + result.removedBytes, 0)) })}</p>
        {receipts.map((result) => <p key={result.environmentId} className="break-words">{environmentName(result.environmentId)} · {formatBytes(result.removedBytes)}{result.reason && ` · ${reasonLabel(result.reason)}`}</p>)}
      </div>}
      <Dialog open={open} onOpenChange={(value) => { if (!cleanup.isPending) setOpen(value); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.desktop.worktrees.cache_preview)}</DialogTitle>
            <DialogDescription>{t(($) => $.desktop.worktrees.cache_preserves)}</DialogDescription>
          </DialogHeader>
          <p className="text-body">{t(($) => $.desktop.worktrees.cache_estimate, { count: eligible.length, size: formatBytes(eligible.reduce((sum, result) => sum + result.sizeBytes, 0)) })}</p>
          <p className="text-caption text-muted-foreground">{t(($) => $.desktop.worktrees.cache_logical_size)}</p>
          {eligible.length === 0 && <p className="text-body">{t(($) => $.desktop.worktrees.cache_empty)}</p>}
          <div className="max-h-72 space-y-3 overflow-y-auto">
            {preview.map((result) => <div key={result.environmentId} className="text-caption">
              <p className="break-words font-medium">{environmentName(result.environmentId)}</p>
              {result.reason ? <p className="text-muted-foreground">{reasonLabel(result.reason)}</p> : result.candidates.length === 0 ? <p className="text-muted-foreground">{t(($) => $.desktop.worktrees.cache_empty)}</p> : result.candidates.map((candidate) => <p key={candidate.path} className="break-all font-mono text-muted-foreground">{candidate.path} · {formatBytes(candidate.sizeBytes)}</p>)}
            </div>)}
          </div>
          <DialogFooter>
            <Button variant="ghost" disabled={cleanup.isPending} onClick={() => setOpen(false)}>{t(($) => $.desktop.daemon.cancel)}</Button>
            <Button disabled={disabled || cleanup.isPending || eligible.length === 0 || cleanup.isError} aria-busy={cleanup.isPending} onClick={() => cleanup.mutate()}>
              {cleanup.isPending && <Loader2 className="size-3.5 animate-spin" />}{t(($) => $.desktop.worktrees.clean_cache)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
