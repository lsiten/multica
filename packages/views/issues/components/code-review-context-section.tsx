"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { GitChangesDialog } from "./git-changes-dialog";
import { useT } from "../../i18n";

export function CodeReviewContextSection({ issueId }: { issueId: string }) {
  const workspaceId = useWorkspaceId();
  return <IssueGitDirectories key={`${workspaceId}:${issueId}`} issueId={issueId} workspaceId={workspaceId} />;
}

function IssueGitDirectories({ issueId, workspaceId }: { readonly issueId: string; readonly workspaceId: string }) {
  const { t } = useT("issues");
  const [selected, setSelected] = useState("");
  const [open, setOpen] = useState(false);
  const tasks = useQuery({ queryKey: issueKeys.tasks(issueId), queryFn: () => api.listTasksByIssue(issueId), staleTime: 30000 });
  const seen = new Set<string>();
  const directories = (tasks.data ?? []).toSorted((a, b) => b.created_at.localeCompare(a.created_at)).filter((task) => {
    if (!task.work_dir || !task.runtime_id) return false;
    const key = JSON.stringify([task.runtime_id, task.work_dir]);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  const task = directories.find((run) => run.id === selected) ?? directories[0];
  return <section className="space-y-2 rounded-lg border bg-card p-3">
    <h3 className="text-caption font-medium">{t(($) => $.git_changes.title)}</h3>
    {tasks.isPending ? <p role="status">{t(($) => $.local_review.loading)}</p> : tasks.error ? <p role="alert">{tasks.error.message}</p> : !task ? <p role="status" className="text-caption text-muted-foreground">{t(($) => $.git_changes.directory_unavailable)}</p> : <>
      <label className="block space-y-1 text-caption">
        <span>{t(($) => $.git_changes.directory)}</span>
        <select value={task.id} onChange={(event) => { setSelected(event.target.value); setOpen(false); }} className="h-8 w-full rounded-md border bg-background px-2">
          {directories.map((run) => <option key={run.id} value={run.id}>{run.work_dir} · {run.runtime_id}</option>)}
        </select>
      </label>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>{t(($) => $.git_changes.open)}</Button>
      {open && <GitChangesDialog key={task.id} request={{ task_id: task.id, workspace_id: workspaceId, runtime_id: task.runtime_id ?? undefined, path: task.work_dir ?? "", target: "" }} onClose={() => setOpen(false)} />}
    </>}
  </section>;
}
