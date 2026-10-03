import { useMemo, useState } from "react";
import { FolderX, Loader2, RefreshCw } from "lucide-react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { agentListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { SettingsCard, SettingsRow, SettingsSection } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { toast } from "sonner";
import type { DaemonStatus, ManagedWorktree } from "../../../shared/daemon-types";
import { LocalReviewDialog, LocalReviewEntry, WorktreeActionLabel, WorktreeLifecycle, WorktreeReason, WorktreeRepositoryStatus } from "@multica/views/issues/components";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { canCleanWorktree, canDiscardWorktree } from "@multica/core/types/managed-worktree";
import { compareWorktreeLifecycle, type WorktreeAction } from "@multica/core/types/worktree-lifecycle";
import { invalidateWorktreeInventory } from "@multica/views/platform";
import { WorktreeCacheManager } from "./worktree-cache-manager";
import { WorktreeInventoryFilters } from "@multica/views/settings/worktree-inventory-filters";
import { WorktreeArchiveManager } from "./worktree-archive-manager";
import { emptyWorktreeFilters, filterWorktreeInventory } from "@multica/core/types/worktree-filters";

export function WorktreeManager({ status }: { status: DaemonStatus }) {
  const { t } = useT("settings");
  const [review, setReview] = useState<LocalReviewRequest | null>(null);
  const userId = useAuthStore((state) => state.user?.id);
  const client = useQueryClient();
  const queryKey = ["desktop-worktrees", userId, status.profile, status.daemonId];
  const enabled = status.state === "running" && !!userId;
  const inventory = useQuery({
    queryKey,
    queryFn: () => window.daemonAPI.listWorktrees(),
    enabled,
    retry: false,
    refetchInterval: enabled ? 30_000 : false,
  });
  const [grouped, setGrouped] = useState(true);
  const [filters, setFilters] = useState(emptyWorktreeFilters);
  const [cachePending, setCachePending] = useState(false);
  const [archivePending, setArchivePending] = useState(false);
  const [pending, setPending] = useState<readonly ManagedWorktree[] | null>(null);
  const [discardChanges, setDiscardChanges] = useState(false);
  const [retained, setRetained] = useState<Record<string, string>>({});
  const selectedWorktree = pending?.[0];
  const singleDiscard = !!selectedWorktree && pending?.length === 1 && canDiscardWorktree(selectedWorktree) && !canCleanWorktree(selectedWorktree);
  const selectCleanup = (selected: readonly ManagedWorktree[]) => {
    setDiscardChanges(false);
    setPending(selected);
  };
  const cleanup = useMutation({
    mutationFn: async (selected: readonly ManagedWorktree[]) => {
      const removedPaths: string[] = [];
      const retained: Record<string, string> = {};
      for (let offset = 0; offset < selected.length; offset += 1000) {
        const result = await window.daemonAPI.cleanupWorktrees(selected.slice(offset, offset + 1000).map((row) => row.path), discardChanges);
        removedPaths.push(...result.removedPaths);
        Object.assign(retained, result.retained);
      }
      return { removedPaths, retained };
    },
    onSuccess: (result) => {
      setRetained(result.retained);
      toast.success(t(($) => $.desktop.worktrees.cleaned, { count: result.removedPaths.length }));
      if (Object.keys(result.retained).length > 0) {
        toast.warning(t(($) => $.desktop.worktrees.retained, { count: Object.keys(result.retained).length }));
      }
      setPending(null);
    },
    onError: (error) => toast.error(t(($) => $.desktop.worktrees.cleanup_failed), { description: error.message }),
    onSettled: () => invalidateWorktreeInventory(client),
  });
  const rows = useMemo(() => filterWorktreeInventory(inventory.data ?? [], filters), [inventory.data, filters]);
  const workspaceIds = useMemo(
    () => [...new Set((inventory.data ?? []).map((row) => row.workspaceId).filter(Boolean))],
    [inventory.data],
  );
  const agentQueries = useQueries({
    queries: workspaceIds.map((workspaceId) => ({
      ...agentListOptions(workspaceId),
      enabled,
      retry: false,
    })),
  });
  const groups = useMemo(() => {
    const result = new Map<string, { name: string; action: WorktreeAction; rows: ManagedWorktree[] }>();
    for (const row of rows.toSorted(compareWorktreeLifecycle)) {
      const workspaceIndex = workspaceIds.indexOf(row.workspaceId);
      const currentAgent = agentQueries[workspaceIndex]?.data?.find((agent) => agent.id === row.agentId);
      // Inventory defines local scope; agent identity defines grouping, not its runtime.
      const action = row.nextAction ?? "unknown";
      const key = action + ":" + (grouped ? row.workspaceId + ":" + (row.agentId || row.agentName) : "all");
      const group = result.get(key) ?? { action, name: grouped ? currentAgent?.name || row.agentName || row.agentId || t(($) => $.desktop.worktrees.unknown_agent) : t(($) => $.desktop.worktrees.all), rows: [] };
      group.rows.push(row);
      result.set(key, group);
    }
    return [...result.entries()];
  }, [rows, grouped, t, agentQueries, workspaceIds]);
  const cleanable = (items: readonly ManagedWorktree[]) => items.filter(canCleanWorktree);
  const reasonLabel = (reason: string) => {
    switch (reason) {
      case "active": return t(($) => $.desktop.worktrees.active);
      case "dirty": return t(($) => $.desktop.worktrees.dirty);
      case "unpushed": return t(($) => $.desktop.worktrees.unpushed);
      case "review": return t(($) => $.desktop.worktrees.review_active);
      case "unowned": return t(($) => $.desktop.worktrees.unowned);
      case "output": return t(($) => $.desktop.worktrees.output);
      case "": return t(($) => $.desktop.worktrees.inactive);
      default: return t(($) => $.desktop.worktrees.unavailable);
    }
  };
  const blocked = !enabled || inventory.isError || cleanup.isPending || inventory.isFetching || cachePending || archivePending;
  return (
    <SettingsSection title={t(($) => $.desktop.worktrees.title)} description={t(($) => $.desktop.worktrees.description)}>
      <SettingsCard>
        <SettingsRow label={t(($) => $.desktop.worktrees.count, { count: rows.length })} description={t(($) => $.desktop.worktrees.scope)}>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => void inventory.refetch()} disabled={!enabled || inventory.isFetching || cleanup.isPending}>
              <RefreshCw className={inventory.isFetching ? "size-3.5 animate-spin" : "size-3.5"} />{t(($) => $.desktop.worktrees.refresh)}
            </Button>
            <Button variant="destructive" size="sm" disabled={blocked || cleanable(rows).length === 0} onClick={() => selectCleanup(cleanable(rows))}>
              <FolderX className="size-3.5" />{t(($) => $.desktop.worktrees.clean_all)}
            </Button>
          </div>
        </SettingsRow>
        <SettingsRow label={t(($) => $.desktop.worktrees.group_by_agent)}>
          <Switch checked={grouped} onCheckedChange={setGrouped} aria-label={t(($) => $.desktop.worktrees.group_by_agent)} />
        </SettingsRow>
        <WorktreeInventoryFilters rows={inventory.data ?? []} filters={filters} onChange={setFilters} disabled={cleanup.isPending || cachePending || archivePending} />
        <WorktreeCacheManager key={`cache:${userId}:${status.profile}:${status.daemonId}:${JSON.stringify(filters)}`} rows={rows} disabled={blocked} workspaceId={filters.workspace || undefined} onPendingChange={setCachePending} />
        <WorktreeArchiveManager key={`archive:${userId}:${status.profile}:${status.daemonId}:${JSON.stringify(filters)}`} rows={rows} enabled={enabled} disabled={blocked} filters={filters} onPendingChange={setArchivePending} queryKey={["desktop-environment-archives", userId, status.profile, status.daemonId, filters.workspace]} />
        {!enabled && <p className="px-4 py-3 text-body text-muted-foreground">{t(($) => $.desktop.worktrees.offline)}</p>}
        {inventory.isError && <p role="alert" className="px-4 py-3 text-body text-destructive">{t(($) => $.desktop.worktrees.load_failed)} {inventory.error.message}</p>}
        {Object.keys(retained).length > 0 && <div role="status" className="space-y-2 px-4 py-3"><p className="text-body">{t(($) => $.desktop.worktrees.retained, { count: Object.keys(retained).length })}</p>{Object.entries(retained).map(([path, reason]) => <div key={path}><p className="break-all font-mono text-caption">{path}</p><WorktreeReason reason={reason} /></div>)}</div>}
        {enabled && !inventory.isPending && !inventory.isError && rows.length === 0 && <p className="px-4 py-3 text-body text-muted-foreground">{t(($) => $.desktop.worktrees.empty)}</p>}
        {groups.map(([key, group]) => (
          <div key={key} className="border-t px-4 py-3">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0"><p className="text-caption font-medium"><WorktreeActionLabel action={group.action} /></p><p className="truncate text-body font-medium" title={group.name}>{group.name} · {group.rows.length}</p></div>
              {grouped && <Button variant="outline" size="sm" disabled={blocked || cleanable(group.rows).length === 0} onClick={() => selectCleanup(cleanable(group.rows))}>{t(($) => $.desktop.worktrees.clean_agent)}</Button>}
            </div>
            <div className="mt-2 space-y-3">
              {group.rows.map((row) => (
                <div key={row.path} className="flex items-start gap-3 text-caption text-muted-foreground">
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium text-foreground" title={row.taskName}>{row.taskName}</p>
                    <p className="break-all font-mono" title={row.path}>{row.path}</p>
                    <p>{(row.sizeBytes / 1024 ** 2).toFixed(1)} MB · {reasonLabel(row.active ? "active" : row.protectionReason)}</p>
                    {row.storage && <p>{t(($) => $.desktop.worktrees.storage_breakdown, { code: (row.storage.codeBytes / 1024 ** 2).toFixed(1), output: (row.storage.outputBytes / 1024 ** 2).toFixed(1), logs: (row.storage.logBytes / 1024 ** 2).toFixed(1), runtime: (row.storage.runtimeBytes / 1024 ** 2).toFixed(1) })}</p>}
                    {row.storage?.allocatedBytes != null && <p>{t(($) => $.desktop.worktrees.allocated_size, { size: (row.storage.allocatedBytes / 1024 ** 2).toFixed(1) })}</p>}
                    <WorktreeLifecycle row={row} />
                    {row.repositories?.map((path) => {
                      const repository = row.repositoryDetails?.find((entry) => entry.path === path);
                      return <div key={path} className="mt-2 space-y-1"><WorktreeRepositoryStatus repository={repository} /><LocalReviewEntry labelSuffix={path.split(/[\\/]/).pop()} request={{ task_id: row.taskId, workspace_id: row.workspaceId, runtime_id: row.runtimeId, path, target: repository?.target ?? "" }} onOpen={setReview} /></div>;
                    })}
                  </div>
                  <Button variant="ghost" size="sm" disabled={blocked || (!canCleanWorktree(row) && !canDiscardWorktree(row))} onClick={() => selectCleanup([row])}>{canDiscardWorktree(row) ? t(($) => $.desktop.worktrees.discard_worktree) : t(($) => $.desktop.worktrees.clean)}</Button>
                </div>
              ))}
            </div>
          </div>
        ))}
      </SettingsCard>
      {review && <LocalReviewDialog key={review.path} request={review} onClose={() => setReview(null)} />}
      <Dialog open={pending !== null} onOpenChange={(open) => { if (!open && !cleanup.isPending) setPending(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.desktop.worktrees.confirm_title, { count: pending?.length ?? 0 })}</DialogTitle>
            <DialogDescription>{t(($) => $.desktop.worktrees.confirm_description)}</DialogDescription>
          </DialogHeader>
          <div className="max-h-48 overflow-y-auto break-all font-mono text-caption text-muted-foreground">{pending?.map((row) => <p key={row.path}>{row.path}</p>)}</div>
          {singleDiscard && <label className="flex items-start gap-2 text-body text-destructive">
            <input type="checkbox" checked={discardChanges} disabled={cleanup.isPending} onChange={(event) => setDiscardChanges(event.target.checked)} />
            {t(($) => $.desktop.worktrees.discard_changes)}
          </label>}
          <DialogFooter>
            <Button variant="ghost" disabled={cleanup.isPending} onClick={() => setPending(null)}>{t(($) => $.desktop.daemon.cancel)}</Button>
            <Button variant="destructive" disabled={blocked || (singleDiscard && !discardChanges)} onClick={() => { if (pending) cleanup.mutate(pending); }}>
              {cleanup.isPending && <Loader2 className="size-3.5 animate-spin" />}{t(($) => $.desktop.worktrees.clean)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsSection>
  );
}
