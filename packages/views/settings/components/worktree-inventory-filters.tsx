import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import type { ManagedWorktree } from "@multica/core/types/managed-worktree";
import type { WorktreeFilters } from "@multica/core/types/worktree-filters";
import { useT } from "../../i18n";

type FilterOption = { value: string; label: string };

function InventorySelect({ label, value, options, onChange, disabled }: { label: string; value: string; options: FilterOption[]; onChange: (value: string) => void; disabled: boolean }) {
  return <Select items={options} value={value} disabled={disabled} onValueChange={(next) => onChange(next ?? "")}>
    <SelectTrigger size="sm" aria-label={label}><SelectValue /></SelectTrigger>
    <SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
  </Select>;
}

export function WorktreeInventoryFilters({ rows, filters, onChange, disabled, hideWorkspace = false }: { rows: readonly ManagedWorktree[]; filters: WorktreeFilters; onChange: (filters: WorktreeFilters) => void; disabled: boolean; hideWorkspace?: boolean }) {
  const { t } = useT("settings");
  const options = (key: "workspace" | "project" | "squad" | "agent", label: string) => {
    const values = new Map<string, string>();
    for (const row of rows) {
      const id = key === "workspace" ? row.workspaceId : key === "project" ? row.projectId : key==="agent" ? row.agentId : row.squadId;
      const name = key === "workspace" ? row.workspaceId : key === "project" ? row.projectName : key==="agent" ? row.agentName : row.squadName;
      if (id) values.set(id, name || id);
    }
    if (filters[key] && !values.has(filters[key])) values.set(filters[key], filters[key]);
    return [{ value: "", label }, ...Array.from(values, ([value, label]) => ({ value, label }))];
  };
  const update = (key: keyof WorktreeFilters, value: string) => onChange({ ...filters, [key]: value });
  const kindLabel = (kind: string) => {
    switch (kind) {
      case "issue": return t(($) => $.desktop.worktrees.kind_issue);
      case "chat": return t(($) => $.desktop.worktrees.kind_chat);
      case "autopilot_run": return t(($) => $.desktop.worktrees.kind_autopilot);
      case "quick_create": return t(($) => $.desktop.worktrees.kind_quick_create);
      case "project_supervision": return t(($) => $.desktop.worktrees.kind_supervision);
      default: return t(($) => $.desktop.worktrees.kind_misc);
    }
  };
  return <div className="flex flex-wrap gap-2 px-4 py-3">
    {!hideWorkspace && <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_workspace)} value={filters.workspace} options={options("workspace", t(($) => $.desktop.worktrees.filter_workspace))} onChange={(value) => update("workspace", value)} />}
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_agent)} value={filters.agent} options={options("agent", t(($) => $.desktop.worktrees.filter_agent))} onChange={(value) => update("agent", value)} />
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_project)} value={filters.project} options={options("project", t(($) => $.desktop.worktrees.filter_project))} onChange={(value) => update("project", value)} />
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_squad)} value={filters.squad} options={options("squad", t(($) => $.desktop.worktrees.filter_squad))} onChange={(value) => update("squad", value)} />
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_environment)} value={filters.environment} options={[
      { value: "", label: t(($) => $.desktop.worktrees.filter_environment) }, { value: "git_worktree", label: "Git worktree" }, { value: "directory", label: t(($) => $.desktop.worktrees.directory) },
    ]} onChange={(value) => update("environment", value)} />
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_activity)} value={filters.activity} options={[
      { value: "", label: t(($) => $.desktop.worktrees.filter_activity) }, { value: "active", label: t(($) => $.desktop.worktrees.active) }, { value: "inactive", label: t(($) => $.desktop.worktrees.inactive) }, { value: "protected", label: t(($) => $.desktop.worktrees.protected) },
    ]} onChange={(value) => update("activity", value)} />
    <InventorySelect disabled={disabled} label={t(($) => $.desktop.worktrees.filter_kind)} value={filters.kind} options={[{ value: "", label: t(($) => $.desktop.worktrees.filter_kind) }, ...Array.from(new Set(rows.map((row) => row.kind)), (kind) => ({ value: kind, label: kindLabel(kind) }))]} onChange={(value) => update("kind", value)} />
    <Input disabled={disabled} className="max-w-72" aria-label={t(($) => $.desktop.worktrees.search)} placeholder={t(($) => $.desktop.worktrees.search)} value={filters.search} onChange={(event) => update("search", event.target.value)} />
  </div>;
}
