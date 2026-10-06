import { useState } from "react";
import type { ManagedWorktree } from "@multica/core/types/managed-worktree";
import { paths } from "@multica/core/paths";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { WorktreeLifecycle } from "../../issues/components/worktree-lifecycle";
import { WorktreeInventorySummary } from "./worktree-inventory-summary";

export function RuntimeEnvironmentResources({ rows, workspaceSlug }: { rows: readonly ManagedWorktree[]; workspaceSlug: string }) {
  const { t } = useT("settings");
  const [grouped, setGrouped] = useState(false);
  const groups = new Map<string, ManagedWorktree[]>();
  for (const row of rows) {
    const key = row.agentId || "";
    const group = groups.get(key) ?? [];
    group.push(row);
    groups.set(key, group);
  }
  return <div className="space-y-4">
    <WorktreeInventorySummary rows={rows} />
    <label className="flex items-center gap-2 text-body">
      <input type="checkbox" checked={grouped} onChange={(event) => setGrouped(event.target.checked)} />
      {t(($) => $.desktop.worktrees.group_by_agent)}
    </label>
    <div className="max-h-[32rem] space-y-3 overflow-y-auto">
      {grouped ? Array.from(groups, ([key, group]) => <details key={key} open className="space-y-2">
        <summary className="cursor-pointer text-body font-medium">{group[0]?.agentName || key || "—"} · {group.length}</summary>
        {group.map((row) => <EnvironmentResourceRow key={row.environmentId || row.path} row={row} workspaceSlug={workspaceSlug} />)}
      </details>) : rows.map((row) => <EnvironmentResourceRow key={row.environmentId || row.path} row={row} workspaceSlug={workspaceSlug} />)}
    </div>

  </div>;
}

function EnvironmentResourceRow({ row, workspaceSlug }: { row: ManagedWorktree; workspaceSlug: string }) {
  const { t } = useT("settings");
  const reasons = t(($) => $.desktop.worktrees.retention_reasons, { returnObjects: true });
  const reason = reasons[row.retentionReason as keyof typeof reasons] ?? reasons.unavailable;
  return <div className="space-y-1 rounded-lg border p-3 text-caption">
    <p className="break-words font-medium">{row.taskName} · {row.agentName || row.agentId}</p>
    <p className="break-all font-mono text-muted-foreground">{row.path}</p>
    <p>{reason}</p>
    {row.issueId && <AppLink href={paths.workspace(workspaceSlug).issueDetail(row.issueId)} className="inline-block break-all text-primary underline">
      {t(($) => $.desktop.worktrees.retained_issue, { id: row.issueId })}
    </AppLink>}
    {Array.from(new Set([...row.consumerTaskIds, ...(row.retainedTaskId ? [row.retainedTaskId] : [])])).map((id) => <p key={id} className="break-all text-muted-foreground">{t(($) => $.desktop.worktrees.retained_run, { id })}</p>)}
    <p className="text-muted-foreground">{`${(row.sizeBytes / 1024 ** 2).toFixed(1)} MiB`}</p>
    {row.storage && <>
      <p className="text-muted-foreground">{t(($) => $.desktop.worktrees.storage_breakdown, { code: (row.storage.codeBytes / 1024 ** 2).toFixed(1), output: (row.storage.outputBytes / 1024 ** 2).toFixed(1), logs: (row.storage.logBytes / 1024 ** 2).toFixed(1), runtime: (row.storage.runtimeBytes / 1024 ** 2).toFixed(1) })}</p>
      {row.storage.allocatedBytes !== null && <p className="text-muted-foreground">{t(($) => $.desktop.worktrees.allocated_size, { size: (row.storage.allocatedBytes / 1024 ** 2).toFixed(1) })}</p>}
    </>}
    <WorktreeLifecycle row={row} />
  </div>;
}
