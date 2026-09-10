"use client";

import { useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { LocalReviewEntry } from "./local-review-entry";
import { LocalReviewDialog } from "./local-review-dialog";
import { localReviewInventory } from "../../platform/local-review";
import { probeReviewRepositories } from "../../platform/local-review-probe";
import { useT } from "../../i18n";

export function CodeReviewContextSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const workspaceId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const [selected, setSelected] = useState("");
  const [open, setOpen] = useState(false);
  const [repository, setRepository] = useState("");
  const { data: tasks = [] } = useQuery({ queryKey: issueKeys.tasks(issueId), queryFn: () => api.listTasksByIssue(issueId), staleTime: 30000 });
  const inventory = useQuery({ queryKey: ["local-review-task-worktrees", workspaceId, issueId], queryFn: () => localReviewInventory(issueId), retry: false, refetchInterval: 30000 });
  const possible = tasks.filter((task) => !task.durable_work_dir && inventory.data?.some((row) => row.taskId === task.id && row.workspaceId === workspaceId && row.repositories.length > 0)).toSorted((a, b) => b.created_at.localeCompare(a.created_at));
  const probes = useQueries({ queries: possible.map((run) => {
    const row = inventory.data?.find((entry) => entry.taskId === run.id && entry.workspaceId === workspaceId);
    const path = row?.path || row?.repositories[0] || "";
    const runtimeId = run.runtime_id || row?.runtimeId;
    return {
      queryKey: ["review-entry-repositories", userId, workspaceId, runtimeId, run.id, path],
      queryFn: ({ signal }: { signal: AbortSignal }) => probeReviewRepositories({ task_id: run.id, workspace_id: workspaceId, runtime_id: runtimeId, path, target: "" }, signal),
      enabled: !!userId && !!path, retry: false, networkMode: "always" as const, staleTime: 30000, refetchInterval: 30000,
    };
  }) });
  const seen = new Set<string>();
  const available = possible.flatMap((run, index) => {
    const result = probes[index];
    if (!result?.isSuccess || !result.data.repositories.length) return [];
    const runtimeId = run.runtime_id || inventory.data?.find((row) => row.taskId === run.id && row.workspaceId === workspaceId)?.runtimeId;
    const key = JSON.stringify([runtimeId || run.id, [...result.data.repositories].sort()]);
    if (seen.has(key)) return [];
    seen.add(key);
    return [{ run, repositories: result.data.repositories }];
  });
  const candidates = available.map((entry) => entry.run);
  const task = candidates.find((task) => task.id === selected) ?? candidates[0];
  const repositories = available.find((entry) => entry.run.id === task?.id)?.repositories ?? [];
  const checking = !task && probes.some((probe) => probe.isPending || probe.isFetching);
  const unavailable = probes.some((probe) => probe.isError);
  const path = repositories.find((path) => path === repository) ?? repositories[0];
  return <section className="space-y-2 rounded-lg border bg-card p-3">
    <h3 className="text-caption font-medium">{t(($) => $.local_review.title)}</h3>
    {inventory.isPending || checking ? <p role="status" className="text-caption text-muted-foreground">{t(($) => $.local_review.loading)}</p> : inventory.error ? <p role="alert" className="text-caption text-destructive">{inventory.error.message}</p> : !task ? <p role="status" className="text-caption text-muted-foreground">{unavailable ? t(($) => $.local_review.entry_failed) : t(($) => $.local_review.entry_empty)}</p> : <>
      <select aria-label={t(($) => $.local_review.run)} value={task.id} onChange={(event) => { setSelected(event.target.value); setOpen(false); }} className="w-full rounded border bg-background p-1 text-caption">
        {candidates.map((row) => <option key={row.id} value={row.id}>{row.branch_name || row.id.slice(0, 8)} · {row.status}</option>)}
      </select>
      <p className="break-all font-mono text-caption">{task.branch_name ?? "—"}</p>
      <p className="break-all text-caption text-muted-foreground">{task.work_dir || task.durable_work_dir}</p>
      {repositories.length > 1 && <select className="w-full rounded border bg-background p-1 text-caption" aria-label={t(($) => $.local_review.repository)} value={path} onChange={(event) => { setRepository(event.target.value); setOpen(false); }}>{repositories.map((path) => <option key={path} value={path}>{path}</option>)}</select>}
      {task.durable_work_dir && <p className="text-caption text-muted-foreground">{t(($) => $.local_review.delivery_notice)}</p>}
      {path && <LocalReviewEntry request={{ task_id: task.id, workspace_id: workspaceId, runtime_id: task.runtime_id ?? undefined, path, target: "main" }} onOpen={() => { setSelected(task.id); setRepository(path); setOpen(true); }} />}
      {open && path && selected === task.id && repository === path && <LocalReviewDialog key={task.id + path} request={{ task_id: task.id, workspace_id: workspaceId, runtime_id: task.runtime_id ?? undefined, path, target: "main" }} onClose={() => setOpen(false)} />}
    </>}
  </section>;
}
