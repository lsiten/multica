"use client";

import { FolderOpen } from "@multica/ui/icons";
import type { ChatSessionProject } from "@multica/core/types";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";

export function ChatProjectSelector({
  projects,
  selectedProjectId,
  selectionRequired,
  onSelect,
}: {
  readonly projects: readonly ChatSessionProject[];
  readonly selectedProjectId?: string | null;
  readonly selectionRequired: boolean;
  readonly onSelect: (projectId: string | null) => void;
}) {
  const { t } = useT("chat");
  const selected = projects.find((project) => project.id === selectedProjectId);
  const associated = projects.filter((project) => project.role === "selected" || project.role === "associated");
  const available = projects.filter((project) => project.role === "available" || project.role === "unavailable");
  if (!projects.length) {
    return <p className="rounded-md border border-dashed p-3 text-caption text-muted-foreground">{t(($) => $.doc_file_list.no_projects)}</p>;
  }
  return (
    <div className="space-y-1.5 rounded-md border bg-muted/20 p-2">
      <div className="flex items-center justify-between gap-2 text-caption">
        <span className="font-medium">{t(($) => $.doc_file_list.current_project)}</span>
        {selectionRequired && <span className="text-amber-700 dark:text-amber-300">{t(($) => $.doc_file_list.project_required)}</span>}
      </div>
      <Select
        items={[{ value: "__none__", label: t(($) => $.doc_file_list.select_project) }, ...projects.map((project) => ({ value: project.id, label: project.title }))]}
        value={selected?.id ?? "__none__"}
        onValueChange={(value) => onSelect(value === "__none__" ? null : value)}
      >
        <SelectTrigger className="w-full min-w-0" aria-label={t(($) => $.doc_file_list.switch_project)}>
          <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <SelectValue placeholder={t(($) => $.doc_file_list.select_project)} />
        </SelectTrigger>
        <SelectContent align="start">
          <SelectItem value="__none__">{t(($) => $.doc_file_list.select_project)}</SelectItem>
          {associated.length > 0 && (
            <SelectGroup>
              <SelectLabel>{t(($) => $.doc_file_list.associated_projects)}</SelectLabel>
              {associated.map((project) => <SelectItem key={project.id} value={project.id}>{project.title}</SelectItem>)}
            </SelectGroup>
          )}
          {available.length > 0 && (
            <SelectGroup>
              <SelectLabel>{t(($) => $.doc_file_list.available_projects)}</SelectLabel>
            {available.map((project) => <SelectItem key={project.id} value={project.id} disabled={project.selectable === false || project.role === "unavailable"}>{project.title}</SelectItem>)}
            </SelectGroup>
          )}
        </SelectContent>
      </Select>
      {selected && <p className="truncate text-caption text-muted-foreground">{selected.description || selected.title}</p>}
    </div>
  );
}
