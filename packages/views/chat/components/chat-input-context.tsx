"use client";

import { useId } from "react";
import { TriangleAlert } from "@multica/ui/icons";
import type { Project, Squad } from "@multica/core/types";
import { ProjectPicker } from "../../projects/components/project-picker";
import { ClearablePillButton } from "../../common/pill-button";
import { DeferredTooltip } from "../../common/deferred-tooltip";
import { useT } from "../../i18n";

export function ChatInputContext({ projects, projectId, squads, squadId, workspaceScopeAllowed, enabled, unsupported, onProjectChange, onSquadChange }: {
  readonly projects: Project[];
  readonly projectId?: string | null;
  readonly squads: Squad[];
  readonly squadId?: string | null;
  readonly workspaceScopeAllowed: boolean;
  readonly enabled: boolean;
  readonly unsupported?: boolean;
  readonly onProjectChange?: (id: string | null) => void;
  readonly onSquadChange?: (id: string | null) => void;
}) {
  const { t } = useT("chat");
  const selectId = useId();
  const project = projects.find((item) => item.id === projectId);
  const squad = squads.find((item) => item.id === squadId);
  const showSquads = !!onSquadChange && (squads.length > 0 || !!squad);
  if (!project && !showSquads) return null;
  return (
    <div data-chat-input-context className="flex shrink-0 flex-wrap items-center gap-1.5 px-3 pt-2">
      {project && <div className="flex min-w-0 max-w-full items-center gap-1">
        <ProjectPicker projectId={project.id} projects={projects} onUpdate={(updates) => onProjectChange?.(updates.project_id ?? null)} disabled={!enabled} triggerRender={
          <ClearablePillButton disabled={!enabled} aria-label={t(($) => $.input.change_project_context)} title={t(($) => $.input.change_project_context)} onClear={() => onProjectChange?.(null)} clearLabel={t(($) => $.input.remove_project_context)} className="h-6 max-w-full border-surface-border bg-surface-raised font-medium text-foreground" />
        } />
        {unsupported && <DeferredTooltip openOnFocus content={t(($) => $.input.project_context_unsupported)} trigger={
          <span role="img" tabIndex={0} aria-label={t(($) => $.input.project_context_unsupported)} className="shrink-0 rounded-sm p-1 text-warning focus-visible:ring-2 focus-visible:ring-ring"><TriangleAlert className="size-3.5" aria-hidden="true" /></span>
        } />}
      </div>}
      {showSquads && <div className="flex min-w-0 items-center gap-1.5">
        <label className="text-caption text-muted-foreground" htmlFor={selectId}>{t(($) => $.input.squad_context)}</label>
        <select id={selectId} value={squad?.id ?? ""} onChange={(event) => onSquadChange?.(event.target.value || null)} disabled={!enabled} className="h-6 min-w-0 max-w-40 rounded-md border border-surface-border bg-surface-raised px-1 text-caption">
          {workspaceScopeAllowed && <option value="">{t(($) => $.input.workspace_context)}</option>}
          {squads.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select>
      </div>}
    </div>
  );
}
