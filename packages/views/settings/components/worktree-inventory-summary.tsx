import type { ManagedWorktree } from "@multica/core/types/managed-worktree";
import { summarizeWorktreeInventory } from "@multica/core/types/worktree-inventory";
import { useT } from "../../i18n";

export function WorktreeInventorySummary({ rows }: { rows: readonly ManagedWorktree[] }) {
  const { t } = useT("settings");
  const summary = summarizeWorktreeInventory(rows);
  const labels = t(($) => $.desktop.worktrees.summary, { returnObjects: true });
  return <dl className="grid grid-cols-2 gap-3 sm:grid-cols-3">
    {Object.entries(summary).map(([key, count]) => <div key={key} className="rounded-lg border p-3">
      <dt className="text-caption text-muted-foreground">{labels[key as keyof typeof labels]}</dt>
      <dd className="mt-1 text-body font-medium">{summary.unresolved > 0 && (key === "physicalWorktrees" || key === "codeEnvironments") ? `≥ ${count}` : count}</dd>
    </div>)}
  </dl>;
}
