"use client";

import { useCallback, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueStatusListOptions } from "@multica/core/issue-statuses";
import { projectSupervisionKey } from "@multica/core/projects";
import type { Project, ProjectSupervision, ProjectSupervisionConfig } from "@multica/core/types/project";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@multica/ui/components/ui/sheet";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { Switch } from "@multica/ui/components/ui/switch";
import { useT } from "../../i18n";
import { useStatusLabel } from "../../issues/utils/status-label";
import { useNavigationGuard } from "../../navigation";
import { confirmNavigation } from "../../platform/navigation-confirmation";

const numericFields = ["max_in_flight", "batch_size", "scan_interval_seconds", "stale_after_seconds", "no_progress_limit"] as const;
type SupervisionDraft = Pick<ProjectSupervision, "enabled" | "config" | "revision">;

export function ProjectSupervisionSettings({ project, view }: {
  readonly project: Project;
  readonly view: ProjectSupervision;
}) {
  const { t } = useT("projects");
  const { t: common } = useT("common");
  const wsId = useWorkspaceId();
  const client = useQueryClient();
  const id = useId();
  const statusLabel = useStatusLabel(wsId);
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<SupervisionDraft | null>(null);
  const statuses = useQuery({ ...issueStatusListOptions(wsId), enabled: open });
  const save = useMutation({
    mutationFn: (input: SupervisionDraft) => api.saveProjectSupervision(project.id, input),
    onSuccess: (result) => {
      client.setQueryData(projectSupervisionKey(wsId, project.id), result);
      setDraft(null);
      setOpen(false);
    },
  });
  const guard = useCallback(() => !save.isPending && (!draft || confirmNavigation({ message: t(($) => $.supervision.unsaved), cancelLabel: common(($) => $.cancel), confirmLabel: common(($) => $.confirm) })), [common, draft, save.isPending, t]);
  useNavigationGuard(guard, !!draft || save.isPending);

  const value = draft ?? { enabled: view.enabled, config: view.config, revision: view.revision };
  const update = (patch: Partial<SupervisionDraft>) => { setDraft({ ...value, ...patch }); save.reset(); };
  const updateConfig = (patch: Partial<ProjectSupervisionConfig>) => update({ config: { ...value.config, ...patch } });
  const cancel = () => { setDraft(null); save.reset(); setOpen(false); };

  return (
    <Sheet open={open} onOpenChange={(next) => {
      if (next) setOpen(true);
      else if (guard()) cancel();
    }}>
      <SheetTrigger render={<Button size="sm" variant="outline" />}>{t(($) => $.supervision.configure)}</SheetTrigger>
      <SheetContent className="gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-md" aria-describedby={undefined}>
        <SheetHeader className="shrink-0 pr-12">
          <SheetTitle>{t(($) => $.supervision.configure)}</SheetTitle>
          <p className="truncate text-caption text-muted-foreground">{project.title}</p>
        </SheetHeader>
        <form className="flex min-h-0 flex-1 flex-col" onSubmit={(event) => {
          event.preventDefault();
          if (draft && !save.isPending) save.mutate(draft);
        }}>
          <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-4 text-caption">
            <div className="space-y-2">
              <label htmlFor={`${id}-enabled`} className="flex min-h-8 items-center justify-between gap-4 font-medium">
                {t(($) => $.supervision.enable)}
                <Switch id={`${id}-enabled`} checked={value.enabled} disabled={save.isPending} onCheckedChange={(enabled) => update({ enabled })} />
              </label>
              {project.lead_type !== "agent" && <p className="text-muted-foreground">{t(($) => $.supervision.no_lead)}</p>}
            </div>
            <div className="space-y-2">
              <label htmlFor={`${id}-advance`} className="flex min-h-8 items-center gap-3 font-medium">
                <Checkbox id={`${id}-advance`} checked={value.config.auto_advance} disabled={save.isPending} onCheckedChange={(auto_advance) => updateConfig({ auto_advance })} />
                {t(($) => $.supervision.auto_advance)}
              </label>
              <p className="text-muted-foreground">{t(($) => $.supervision.auto_help)}</p>
            </div>
            <fieldset disabled={save.isPending} className="space-y-4">
              {numericFields.map((field) => <label key={field} htmlFor={`${id}-${field}`} className="flex items-center justify-between gap-4">
                <span className="min-w-0">{t(($) => $.supervision.fields[field])}</span>
                <Input id={`${id}-${field}`} type="number" className="w-24 shrink-0 text-caption" min={field === "scan_interval_seconds" || field === "stale_after_seconds" ? 60 : 1} step={1} required value={value.config[field]} onChange={(event) => updateConfig({ [field]: Number(event.target.value) })} />
              </label>)}
            </fieldset>
            <fieldset disabled={save.isPending} className="space-y-2">
              <legend className="mb-2 font-medium">{t(($) => $.supervision.ready_statuses)}</legend>
              {statuses.isPending && <p role="status" className="text-muted-foreground">{t(($) => $.supervision.loading)}</p>}
              {statuses.isError && <div role="alert" className="space-y-2"><p>{t(($) => $.supervision.unavailable)}</p><Button size="sm" variant="outline" onClick={() => void statuses.refetch()}>{t(($) => $.supervision.retry)}</Button></div>}
              {statuses.data?.filter((status) => status.category === "unstarted" && status.key !== "backlog" && status.key !== "triage" && !status.archived_at).map((status) => <label key={status.key} htmlFor={`${id}-${status.key}`} className="flex min-h-8 items-center gap-3">
                <Checkbox id={`${id}-${status.key}`} checked={value.config.ready_statuses.includes(status.key)} onCheckedChange={(checked) => updateConfig({ ready_statuses: checked ? [...value.config.ready_statuses, status.key] : value.config.ready_statuses.filter((key) => key !== status.key) })} />
                {statusLabel(status.key)}
              </label>)}
            </fieldset>
          </div>
          <SheetFooter className="shrink-0">
            {save.isError && <p role="alert" className="text-caption text-destructive">{t(($) => $.supervision.save_failed)}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="outline" disabled={save.isPending} onClick={cancel}>{t(($) => $.supervision.cancel)}</Button>
              <Button type="submit" disabled={!draft || save.isPending} aria-busy={save.isPending}>
                {save.isPending && <Spinner aria-hidden="true" />}{t(($) => $.supervision.save)}
              </Button>
            </div>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
