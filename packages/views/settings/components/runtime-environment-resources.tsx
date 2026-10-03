import type { ManagedWorktree } from "@multica/core/types/managed-worktree";
import type { WorktreeArchiveSummary } from "@multica/core/types/worktree-archives";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { WorktreeLifecycle } from "../../issues/components/worktree-lifecycle";
import { useState } from "react";

export function RuntimeEnvironmentResources({ rows, history, historyOpen, setHistoryOpen, historyError, disabled, onRestore }: {
  rows: readonly ManagedWorktree[]; history: readonly WorktreeArchiveSummary[]; historyOpen: boolean; setHistoryOpen: (open: boolean) => void;
  historyError: Error | null; disabled: boolean; onRestore: (row: WorktreeArchiveSummary) => void;
}) {
  const { t } = useT("settings");
  const [grouped,setGrouped]=useState(false);
  const groups=new Map<string,ManagedWorktree[]>();
  for(const row of rows) { const key=row.agentId || ""; const group=groups.get(key) ?? []; group.push(row); groups.set(key,group); }
  return <div className="space-y-4">
    <p className="text-body">{t(($) => $.desktop.worktrees.count, { count: rows.length })}</p>
    <label className="flex items-center gap-2 text-body"><input type="checkbox" checked={grouped} onChange={(event)=>setGrouped(event.target.checked)} />{t(($)=>$.desktop.worktrees.group_by_agent)}</label>
    <div className="max-h-[32rem] space-y-3 overflow-y-auto">
      {grouped ? Array.from(groups,([key,group])=><details key={key} open className="space-y-2"><summary className="cursor-pointer text-body font-medium">{group[0]?.agentName || key || "—"} · {group.length}</summary>{group.map((row)=><EnvironmentResourceRow key={row.environmentId||row.path} row={row}/>)}</details>) : rows.map((row)=><EnvironmentResourceRow key={row.environmentId||row.path} row={row}/>)}
    </div>
    <details open={historyOpen} onToggle={(event) => setHistoryOpen(event.currentTarget.open)}>
      <summary className="cursor-pointer text-body font-medium">{t(($) => $.desktop.worktrees.archive_history)}</summary>
      {historyError && <p role="alert" className="py-2 text-destructive">{historyError.message}</p>}
      <div className="max-h-80 space-y-3 overflow-y-auto py-3">
        {history.map((row) => <div key={row.archiveId} className="flex gap-3 rounded-lg border p-3 text-caption"><div className="min-w-0 flex-1"><p className="break-words font-medium">{row.taskName} · {row.agentName}</p><p className="break-all font-mono">{row.originalPath}</p><p>{`${(row.archiveBytes / 1024 ** 2).toFixed(1)} MiB`}</p>{row.restoreReason && <p className="text-muted-foreground">{t(($) => $.desktop.worktrees.restore_existing)}</p>}</div><Button variant="outline" size="sm" disabled={disabled || !!row.restoreReason} onClick={() => onRestore(row)}>{t(($) => $.desktop.worktrees.restore_archive)}</Button></div>)}
      </div>
    </details>
  </div>;
}

function EnvironmentResourceRow({row}:{row:ManagedWorktree}) {
 const {t}=useT("settings");
 return <div className="space-y-1 rounded-lg border p-3 text-caption">
  <p className="break-words font-medium">{row.taskName} · {row.agentName || row.agentId}</p>
  <p className="break-all font-mono text-muted-foreground">{row.path}</p>
  <p className="text-muted-foreground">{`${(row.sizeBytes/1024**2).toFixed(1)} MiB · `}{row.active?t(($)=>$.desktop.worktrees.active):row.protectionReason?t(($)=>$.desktop.worktrees.protected):t(($)=>$.desktop.worktrees.inactive)}</p>
  {row.storage && <><p className="text-muted-foreground">{t(($)=>$.desktop.worktrees.storage_breakdown,{code:(row.storage.codeBytes/1024**2).toFixed(1),output:(row.storage.outputBytes/1024**2).toFixed(1),logs:(row.storage.logBytes/1024**2).toFixed(1),runtime:(row.storage.runtimeBytes/1024**2).toFixed(1)})}</p>{row.storage.allocatedBytes!==null && <p className="text-muted-foreground">{t(($)=>$.desktop.worktrees.allocated_size,{size:(row.storage.allocatedBytes/1024**2).toFixed(1)})}</p>}</>}
  <WorktreeLifecycle row={row}/>
 </div>;
}
