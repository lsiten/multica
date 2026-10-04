"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace } from "@multica/core/paths";
import { environmentOperationBatches, environmentOperationID, type EnvironmentOperationRequest, type EnvironmentOperationStatus } from "@multica/core/types/environment-operations";
import type { WorktreeCacheResult } from "@multica/core/types/managed-worktree";
import type { WorktreeArchiveResult, WorktreeArchiveSummary } from "@multica/core/types/worktree-archives";
import { Button } from "@multica/ui/components/ui/button";
import { emptyWorktreeFilters, filterWorktreeArchives, filterWorktreeInventory } from "@multica/core/types/worktree-filters";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { RefreshCw } from "lucide-react";
import { useT } from "../../i18n";
import { SettingsTab } from "./settings-layout";
import { RuntimeEnvironmentResources } from "./runtime-environment-resources";
import { RuntimeEnvironmentPolicy } from "./runtime-environment-policy";
import { WorktreeInventoryFilters } from "./worktree-inventory-filters";
import { RuntimeEnvironmentPicker } from "./runtime-environment-picker";

type EnvironmentPreview = { kind: "cache"; rows: WorktreeCacheResult[] } | { kind: "archive"; rows: WorktreeArchiveResult[] } | { kind: "restore"; row: WorktreeArchiveSummary };

export function RuntimeEnvironmentsTab() {
  const workspace = useCurrentWorkspace();
  const userId = useAuthStore((state) => state.user?.id);
  const { t } = useT("settings");
  return <SettingsTab title={t(($) => $.environments.title)} scope="workspace">
    {workspace && userId && <RuntimeEnvironments key={`${userId}:${workspace.id}`} workspaceId={workspace.id} workspaceSlug={workspace.slug} userId={userId} />}
  </SettingsTab>;
}

export function RuntimeEnvironments({ workspaceId, workspaceSlug, userId }: { workspaceId: string; workspaceSlug: string; userId: string }) {
  const { t } = useT("settings");
  const client = useQueryClient();
  const [runtimeId, setRuntimeId] = useState("");
  const [filters, setFilters] = useState(emptyWorktreeFilters);
  const [preview, setPreview] = useState<EnvironmentPreview | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [jobId, setJobId] = useState("");
  const pendingBatches = useRef<EnvironmentOperationRequest[]>([]);
  const advancedJob = useRef("");
  const prefix = ["workspaces", workspaceId, "runtime-environments", userId];
  const runtimes = useQuery({ queryKey: [...prefix, "runtimes"], queryFn: () => api.listEnvironmentRuntimes(workspaceId, workspaceSlug), retry: false });
  const owned = (runtimes.data ?? []).filter((runtime) => runtime.workspace_id === workspaceId && runtime.owner_id === userId && runtime.runtime_mode === "local");
  const selected = owned.find((runtime) => runtime.id === runtimeId);
  const ready = !!selected && selected.status === "online";
  const inventory = useQuery({ queryKey: [...prefix, runtimeId, "inventory"], queryFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "inventory" }), enabled: ready, retry: false });
  const history = useQuery({ queryKey: [...prefix, runtimeId, "archives"], queryFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "archives" }), enabled: ready && historyOpen, retry: false });
  const operations = useQuery({
    queryKey: [...prefix, runtimeId, "operations"], queryFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "operations" }),
    enabled: ready, retry: false,
    refetchInterval: (query) => query.state.data?.some((item) => ["running", "cancelling"].includes(item.status)) ? 2000 : 10000,
  });
  const selectedJobId = jobId || operations.data?.find((item) => ["running", "cancelling"].includes(item.status))?.id || "";
  const job = useQuery({
    queryKey: [...prefix, runtimeId, "operation", selectedJobId], queryFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "operation_status", operation_id: selectedJobId }),
    enabled: ready && !!selectedJobId, retry: false,
    refetchInterval: (query) => query.state.data && ["running", "cancelling"].includes(query.state.data.status) ? 2000 : false,
  });
  const scan = useMutation({
    mutationFn: async (kind: "cache" | "archive") => kind === "cache"
      ? { kind, rows: await api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "cache_preview" }) }
      : { kind, rows: await api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "archive_preview" }) },
    onSuccess: (result) => setPreview(result),
  });
  const rememberJob = (status: EnvironmentOperationStatus) => {
    setJobId(status.id);
    client.setQueryData([...prefix, runtimeId, "operation", status.id], status);
  };
  const start = useMutation({
    mutationFn: (request: EnvironmentOperationRequest) => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "operation_start", operation: request }),
    onSuccess: (status) => { rememberJob(status); setPreview(null); void operations.refetch(); },
    onError: () => { pendingBatches.current = []; },
  });
  const cancel = useMutation({ mutationFn: () => api.executeRuntimeEnvironment(workspaceId, runtimeId, { action: "operation_cancel", operation_id: selectedJobId }), onSuccess: (status) => { rememberJob(status); void operations.refetch(); } });
  const startOperation = start.mutate;
  useEffect(() => {
    const status = job.data;
    if (start.isPending || !status || ["running", "cancelling"].includes(status.status) || advancedJob.current === status.id) return;
    advancedJob.current = status.id;
    if (status.status !== "completed") { pendingBatches.current = []; return; }
    const next = pendingBatches.current.shift();
    if (next) startOperation(next);
  }, [job.data, start.isPending, startOperation]);
  const running = !!job.data && ["running", "cancelling"].includes(job.data.status);
  const otherRunning = operations.data?.some((item) => ["running", "cancelling"].includes(item.status)) ?? false;
  const busy = scan.isPending || start.isPending || cancel.isPending || running || otherRunning;
  const error = runtimes.error ?? inventory.error ?? operations.error ?? scan.error ?? start.error ?? cancel.error ?? job.error;
  const visibleRows = filterWorktreeInventory(inventory.data ?? [], { ...filters, workspace: workspaceId });
  const eligible = preview && preview.kind !== "restore" ? preview.rows.filter((row) => !row.reason && row.revision && visibleRows.some((item) => item.environmentId === row.environmentId) && (preview.kind !== "cache" || ("candidates" in row && row.candidates.length > 0))) : [];
  const executePreview = () => {
    if (!preview) return;
    if (preview.kind === "restore") {
      start.mutate({ id: environmentOperationID(), action: "restore", selections: [], archive_id: preview.row.archiveId });
    } else {
      const batches = environmentOperationBatches(preview.kind === "cache" ? "clean_cache" : "archive", eligible.map((row) => ({ environment_id: row.environmentId, revision: row.revision })));
      const first = batches.shift();
      if (!first) return;
      pendingBatches.current = batches;
      advancedJob.current = "";
      start.mutate(first);
    }
  };
  const reasonLabel = (reason: string) => {
    switch (reason) {
      case "active": return t(($) => $.desktop.worktrees.active);
      case "review": return t(($) => $.desktop.worktrees.review_active);
      case "scope_changed": case "unowned": return t(($) => $.desktop.worktrees.unowned);
      case "preview_changed": return t(($) => $.desktop.worktrees.archive_changed);
      case "existing_environment": return t(($) => $.desktop.worktrees.restore_existing);
      case "reclaim_failed": return t(($) => $.desktop.worktrees.archive_reclaim_failed);
      default: return t(($) => $.desktop.worktrees.unavailable);
    }
  };
  return <div className="space-y-5">
    <RuntimeEnvironmentPicker runtimes={owned} runtimeId={runtimeId} disabled={busy} onChange={(value) => {
      setRuntimeId(value); setFilters(emptyWorktreeFilters); setJobId(""); setPreview(null);
      pendingBatches.current = []; advancedJob.current = "";
      scan.reset(); start.reset(); cancel.reset();
    }} />
    <div className="flex flex-wrap items-center gap-3">
      <Button variant="outline" size="sm" disabled={!ready || busy || inventory.isFetching} onClick={() => { void inventory.refetch(); if (historyOpen) void history.refetch(); }}><RefreshCw className="size-3.5" />{t(($) => $.desktop.worktrees.refresh)}</Button>
    </div>
    {selected && !ready && <p className="text-body text-muted-foreground">{t(($) => $.environments.runtime_offline)}</p>}
    {runtimes.isSuccess && owned.length === 0 && <p className="text-body text-muted-foreground">{t(($) => $.environments.no_owned_runtimes)}</p>}
    {error && <p role="alert" className="break-words text-body text-destructive">{error.message}</p>}
    {ready && <>
      <RuntimeEnvironmentPolicy key={runtimeId} workspaceId={workspaceId} runtimeId={runtimeId} userId={userId} disabled={busy} />
      {!!operations.data?.length && <Select disabled={start.isPending || pendingBatches.current.length > 0} items={[{ value: "", label: t(($) => $.environments.operation_history) }, ...operations.data.map((item) => ({ value: item.id, label: `${item.automatic ? t(($) => $.environments.automatic) : t(($) => $.environments.manual)} · ${new Date(item.startedAt).toLocaleString()} · ${item.completed}/${item.total}` }))]} value={selectedJobId} onValueChange={(value) => setJobId(value ?? "")}>
        <SelectTrigger aria-label={t(($) => $.environments.operation_history)}><SelectValue /></SelectTrigger>
        <SelectContent><SelectItem value="">{t(($) => $.environments.operation_history)}</SelectItem>{operations.data.map((item) => <SelectItem key={item.id} value={item.id}>{item.automatic ? t(($) => $.environments.automatic) : t(($) => $.environments.manual)} · {new Date(item.startedAt).toLocaleString()} · {item.completed}/{item.total}</SelectItem>)}</SelectContent>
      </Select>}
      <WorktreeInventoryFilters rows={inventory.data ?? []} filters={filters} onChange={setFilters} disabled={busy} hideWorkspace />
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" disabled={busy || !inventory.isSuccess} onClick={() => { start.reset(); scan.mutate("cache"); }}>{t(($) => $.desktop.worktrees.scan_cache)}</Button>
        <Button variant="outline" size="sm" disabled={busy || !inventory.isSuccess} onClick={() => { start.reset(); scan.mutate("archive"); }}>{t(($) => $.desktop.worktrees.scan_archive)}</Button>
      </div>
      {job.data && <div role="status" className="space-y-2 rounded-lg border p-3 text-caption">
        <p>{t(($) => $.environments.operation_progress, { done: job.data.completed, total: job.data.total })} · {t(($) => $.environments.status[job.data.status])}</p>
        <p className="break-all font-mono">{job.data.id}</p>
        {pendingBatches.current.length > 0 && <p>{t(($) => $.environments.batch_pending, { count: pendingBatches.current.reduce((count, batch) => count + batch.selections.length, 0) })}</p>}
        {job.data.error && <p className="text-destructive">{job.data.error}</p>}
        {job.data.results.map((result, index) => <p key={`${result.environmentId}:${index}`} className="break-words">{inventory.data?.find((row) => row.environmentId === result.environmentId)?.taskName || result.environmentId || result.archiveId} · {result.reason ? reasonLabel(result.reason) : result.restored ? t(($) => $.desktop.worktrees.archive_restored) : result.reclaimed ? t(($) => $.desktop.worktrees.archive_reclaimed, { size: `${(result.archiveBytes / 1024 ** 2).toFixed(1)} MiB` }) : t(($) => $.desktop.worktrees.cache_removed, { count: result.removedCount, size: `${(result.removedBytes / 1024 ** 2).toFixed(1)} MiB` })}</p>)}
        {running && <Button variant="outline" size="sm" disabled={cancel.isPending || job.data.status === "cancelling"} onClick={() => cancel.mutate()}>{t(($) => $.environments.cancel_operation)}</Button>}
        {!running && <Button variant="outline" size="sm" onClick={() => { void inventory.refetch(); if (historyOpen) void history.refetch(); }}>{t(($) => $.desktop.worktrees.refresh)}</Button>}
      </div>}
      <RuntimeEnvironmentResources rows={visibleRows} history={filterWorktreeArchives(history.data ?? [], { ...filters, workspace: workspaceId })} historyOpen={historyOpen} setHistoryOpen={setHistoryOpen} historyError={history.error} disabled={busy} onRestore={(row) => { start.reset(); setPreview({ kind: "restore", row }); }} />
    </>}
    <Dialog open={preview !== null} onOpenChange={(open) => { if (!open && !start.isPending) setPreview(null); }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{preview?.kind === "cache" ? t(($) => $.desktop.worktrees.cache_preview) : preview?.kind === "restore" ? t(($) => $.desktop.worktrees.restore_archive) : t(($) => $.desktop.worktrees.archive_preview)}</DialogTitle><DialogDescription>{preview?.kind === "cache" ? t(($) => $.desktop.worktrees.cache_preserves) : preview?.kind === "restore" ? t(($) => $.desktop.worktrees.restore_preserves) : t(($) => $.desktop.worktrees.archive_preserves)}</DialogDescription></DialogHeader>
        {preview?.kind === "restore" ? <p className="break-all font-mono text-caption">{preview.row.originalPath}</p> : <div className="max-h-64 space-y-2 overflow-y-auto text-caption">
          {preview?.rows.filter((row) => visibleRows.some((item) => item.environmentId === row.environmentId)).map((row) => <p className="break-words" key={row.environmentId}>{visibleRows.find((item) => item.environmentId === row.environmentId)?.taskName} · {row.reason ? reasonLabel(row.reason) : `${((("sizeBytes" in row ? row.sizeBytes : row.originalBytes)) / 1024 ** 2).toFixed(1)} MiB`}</p>)}
          {eligible.length === 0 && <p>{t(($) => $.desktop.worktrees.archive_none_eligible)}</p>}
        </div>}
        <DialogFooter><Button variant="ghost" disabled={start.isPending} onClick={() => setPreview(null)}>{t(($) => $.desktop.daemon.cancel)}</Button><Button disabled={start.isPending || start.isError || (preview?.kind !== "restore" && eligible.length === 0)} onClick={executePreview}>{preview?.kind === "cache" ? t(($) => $.desktop.worktrees.clean_cache) : preview?.kind === "restore" ? t(($) => $.desktop.worktrees.restore_archive) : t(($) => $.desktop.worktrees.archive_reclaim)}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}
