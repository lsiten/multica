"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { issueTasksOptions } from "@multica/core/issues/queries";
import { useTaskMessages } from "@multica/core/chat/queries";
import { formatDateOnly, isPastDateOnly } from "@multica/core/issues/date";
import type { ProgressEntry, ProgressIssueRef, ProgressSummary } from "@multica/core/types/issue-progress";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { AppLink } from "../../navigation";
import { HumanRequestCard } from "../../common/human-request-card";
import { AgentTranscriptDialog } from "../../common/task-transcript/agent-transcript-dialog";
import { buildTimeline } from "../../common/task-transcript/build-timeline";
import { useTimeAgo } from "../../i18n";
import { useStatusLabel } from "../utils/status-label";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkProgressLabels } from "./work-progress-labels";
import { WorkProgressActions } from "./work-progress-actions";

export function WorkProgressSummary({ summary, complete }: { summary: ProgressSummary; complete: boolean }) {
  const { t } = useWorkProgressLabels();
  return <div className="flex flex-wrap gap-x-4 gap-y-1 text-caption" aria-label={t($ => $.work_progress.summary)}>
    {!complete && <span className="text-destructive">{t($ => $.work_progress.partial_counts)}</span>}
    <span>{t($ => $.work_progress.done, { count: summary.done })}</span>
    <span className="text-muted-foreground">{t($ => $.work_progress.closed, { count: summary.closed })}</span>
    <span className="font-medium">{t($ => $.work_progress.open, { count: summary.open })}</span>
    <span className="text-muted-foreground">{t($ => $.work_progress.open_breakdown, { leaf: summary.open_leaf, parent: summary.open_parent })}</span>
  </div>;
}

function IssueReference({ issue }: { issue: ProgressIssueRef }) {
  const paths = useWorkspacePaths();
  return <AppLink href={paths.issueDetail(issue.identifier || issue.id)} className="rounded-sm text-foreground underline decoration-border underline-offset-4 hover:decoration-foreground focus-visible:outline focus-visible:outline-ring">{issue.identifier} · {issue.title}</AppLink>;
}

export function WorkProgressEntry({ entry, currentProjectId, requestLinksOnly = false }: { entry: ProgressEntry; currentProjectId?: string | null; requestLinksOnly?: boolean }) {
  const { t, reasonLabel } = useWorkProgressLabels();
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const statusLabel = useStatusLabel(wsId);
  const { getActorName } = useActorName();
  const timeAgo = useTimeAgo();
  const [runOpen, setRunOpen] = useState(false);
  const fullPath = entry.path.length > 3;
  const path = fullPath ? entry.path.slice(-3) : entry.path;
  const crossProject = currentProjectId !== undefined && entry.issue.project_id !== currentProjectId;
  const distinctRootBlockers = entry.root_blockers.length > 0 && (entry.root_blockers.length !== entry.direct_blockers.length || entry.root_blockers.some(root => !entry.direct_blockers.some(direct => direct.id === root.id)));
  return <article className="space-y-2 rounded-lg border bg-card p-3 text-caption" data-progress-issue-id={entry.issue.id}>
    {path.length > 0 && <div className="flex flex-wrap items-center gap-x-1 gap-y-1 break-words text-micro text-muted-foreground" aria-label={t($ => $.work_progress.path)}>
      {fullPath && <details><summary className="cursor-pointer">{t($ => $.work_progress.full_path)}</summary><div className="space-y-1 py-2">{entry.path.map(ancestor => <div key={ancestor.id}><IssueReference issue={ancestor} /></div>)}</div></details>}
      {path.map((ancestor, index) => <span key={ancestor.id}>{index > 0 && <span aria-hidden="true"> / </span>}<IssueReference issue={ancestor} /></span>)}
    </div>}
    <div className="flex flex-wrap items-start justify-between gap-2">
      <div className="min-w-0 break-words text-body font-medium"><IssueReference issue={entry.issue} /></div>
      <span className="shrink-0 rounded-md bg-muted px-2 py-0.5 text-micro">{entry.issue.status_name || statusLabel(entry.issue.status)}</span>
    </div>
    {entry.reasons.length > 0 && <ul className="flex flex-wrap gap-1.5" aria-label={t($ => $.work_progress.reason)}>{entry.reasons.map(reason => <li key={reason} className={`rounded-md px-2 py-0.5 text-micro ${["dependency_cycle", "hierarchy_cycle", "parent_closed_with_open_children", "run_failed", "explicit_block"].includes(reason) ? "bg-destructive/10 text-destructive" : "bg-muted text-muted-foreground"}`}>{reasonLabel(reason)}</li>)}</ul>}
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
      <span>{entry.assignee_id ? getActorName(entry.assignee_type, entry.assignee_id) : t($ => $.work_progress.no_assignee)}</span>
      {entry.stage != null && <span>{t($ => $.work_progress.stage, { stage: entry.stage })}</span>}
      {entry.due_date && <span className={isPastDateOnly(entry.due_date) ? "text-destructive" : ""}>{t($ => $.work_progress.due, { date: formatDateOnly(entry.due_date) })}</span>}
      {crossProject && <span>{t($ => $.work_progress.other_project, { project: entry.issue.project_title || t($ => $.work_progress.no_project) })}</span>}
      {entry.waiting_children > 0 && <span>{t($ => $.work_progress.waiting_children, { count: entry.waiting_children })}</span>}
      {entry.blocked_issue_count > 0 && <span>{t($ => $.work_progress.blocks, { count: entry.blocked_issue_count })}</span>}
      {entry.affected_goal_count > 0 && <span>{t($ => $.work_progress.belongs_to, { count: entry.affected_goal_count })}</span>}
      {entry.wait_since && <span title={new Date(entry.wait_since).toLocaleString()}>{t($ => $.work_progress.since, { time: timeAgo(entry.wait_since) })}</span>}
    </div>
    {entry.direct_blockers.length > 0 && <div className="space-y-1 break-words">
      <p className="text-muted-foreground">{t($ => $.work_progress.direct_blockers)}</p>
      <ul className="space-y-1">{entry.direct_blockers.map(blocker => <li key={blocker.id}><IssueReference issue={blocker} /></li>)}</ul>
      {distinctRootBlockers && <><p className="pt-1 font-medium">{t($ => $.work_progress.root_blockers)}</p><ul className="space-y-1">{entry.root_blockers.map(blocker => <li key={blocker.id}><IssueReference issue={blocker} /><span className="ml-2 text-muted-foreground">{blocker.status_name || statusLabel(blocker.status)}</span></li>)}</ul></>}
    </div>}
    {entry.actions?.length ? <WorkProgressActions entry={entry} /> : null}
    {entry.run && <div className="flex flex-wrap items-center gap-2">
      <span className="text-muted-foreground">{t($ => $.work_progress.run)}: {runStateLabel(entry.run.status, t)}</span>
      <Button size="sm" variant="outline" onClick={() => setRunOpen(true)}>{t($ => $.work_progress.view_run)}</Button>
    </div>}
    {entry.requests.map(request => requestLinksOnly ? <p key={request.id}><AppLink className="rounded-sm underline underline-offset-4" href={`${paths.issueDetail(entry.issue.identifier || entry.issue.id)}#comment-${request.id}`}>{request.needs_me ? t($ => $.work_progress.handle_request) : t($ => $.work_progress.waiting_member, { member: getActorName("member", request.recipient_id) })}</AppLink></p> : <details key={request.id}><summary className="cursor-pointer rounded-sm text-foreground focus-visible:outline focus-visible:outline-ring">{request.needs_me ? t($ => $.work_progress.handle_request) : t($ => $.work_progress.waiting_member, { member: getActorName("member", request.recipient_id) })}</summary><div className="mt-2"><HumanRequestCard requestId={request.id} /></div></details>)}
    {runOpen && entry.run && <ProgressRunDialog issueId={entry.issue.id} runId={entry.run.id} onClose={() => setRunOpen(false)} />}
  </article>;
}

function runStateLabel(status: string, t: ReturnType<typeof useWorkProgressLabels>["t"]): string {
  switch (status) {
    case "queued": case "deferred": return t($ => $.work_progress.run_states.queued);
    case "dispatched": return t($ => $.work_progress.run_states.dispatched);
    case "running": return t($ => $.work_progress.run_states.running);
    case "waiting_local_directory": return t($ => $.work_progress.run_states.waiting_local_directory);
    case "completed": return t($ => $.work_progress.run_states.completed);
    case "failed": return t($ => $.work_progress.run_states.failed);
    case "cancelled": return t($ => $.work_progress.run_states.cancelled);
    default: return t($ => $.work_progress.run_states.unknown);
  }
}

function ProgressRunDialog({ issueId, runId, onClose }: { issueId: string; runId: string; onClose: () => void }) {
  const { t } = useWorkProgressLabels();
  const tasks = useQuery(issueTasksOptions(issueId));
  const task = tasks.data?.find(run => run.id === runId);
  const live = !!task && ["queued", "deferred", "dispatched", "running", "waiting_local_directory"].includes(task.status);
  const messages = useTaskMessages(runId, live, !!task);
  const { getActorName } = useActorName();
  if (!task) return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent><DialogHeader><DialogTitle>{t($ => $.work_progress.view_run)}</DialogTitle></DialogHeader><p role={tasks.isPending ? "status" : "alert"}>{tasks.isPending ? t($ => $.work_progress.loading) : t($ => $.work_progress.run_unavailable)}</p>{tasks.isError && <Button variant="outline" onClick={() => void tasks.refetch()}>{t($ => $.work_progress.retry)}</Button>}</DialogContent></Dialog>;
  return <AgentTranscriptDialog open onOpenChange={open => { if (!open) onClose(); }} task={task} items={buildTimeline(messages.data ?? [])} agentName={getActorName("agent", task.agent_id)} isLive={live} finalFocus contentState={messages.isPending ? <p role="status">{t($ => $.work_progress.loading)}</p> : messages.isError ? <div role="alert">{t($ => $.work_progress.unavailable)} <Button variant="outline" size="sm" onClick={() => void messages.refetch()}>{t($ => $.work_progress.retry)}</Button></div> : undefined} />;
}
