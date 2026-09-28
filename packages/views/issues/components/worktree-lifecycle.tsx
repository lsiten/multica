import type { WorktreeAction, WorktreeLifecycle as Lifecycle } from "@multica/core/types/worktree-lifecycle";
import { useT } from "../../i18n";
import { isBuiltInIssueStatus } from "@multica/core/issue-statuses";

export function WorktreeActionLabel({ action }: { readonly action: WorktreeAction }) {
  const { t } = useT("issues");
  return <span>{t(($) => $.worktree_lifecycle.actions[action])}</span>;
}

export function WorktreeLifecycle({ row }: { readonly row: Lifecycle }) {
  const { t } = useT("issues");
  const runLabels: Record<string, string> = {
    queued: t(($) => $.execution_log.status_queued), deferred: t(($) => $.execution_log.status_queued),
    dispatched: t(($) => $.execution_log.status_dispatched), running: t(($) => $.execution_log.status_running),
    waiting_local_directory: t(($) => $.execution_log.status_waiting_local_directory),
    completed: t(($) => $.execution_log.status_completed), failed: t(($) => $.execution_log.status_failed),
    cancelled: t(($) => $.execution_log.status_cancelled),
  };
  const statusKey = row.issueStatus;
  const issueStatus = isBuiltInIssueStatus(statusKey) ? t(($) => $.status[statusKey]) : statusKey;
  return <div className="space-y-1 text-caption text-muted-foreground">
    {row.stale && <p className="text-warning">{t(($) => $.worktree_lifecycle.stale)}</p>}
    {row.issueId && <p className="break-all" title={row.issueStatus}>{t(($) => $.worktree_lifecycle.issue, { id: row.issueId, status: issueStatus || "—" })}</p>}
    {row.runStatus && <p title={row.runStatus}>{t(($) => $.worktree_lifecycle.run, { status: runLabels[row.runStatus] ?? row.runStatus })}</p>}
    {row.completedAt && <p>{t(($) => $.worktree_lifecycle.completed)} <time dateTime={row.completedAt}>{new Date(row.completedAt).toLocaleString()}</time></p>}
    {row.lastActivityAt && <p>{t(($) => $.worktree_lifecycle.activity)} <time dateTime={row.lastActivityAt}>{new Date(row.lastActivityAt).toLocaleString()}</time></p>}
  </div>;
}

export function WorktreeReason({ reason }: { readonly reason: string }) {
  const { t } = useT("issues");
  const hints: Record<string, string> = {
    dirty: t(($) => $.worktree_lifecycle.dirty), unpushed: t(($) => $.worktree_lifecycle.unpushed),
    output: t(($) => $.worktree_lifecycle.save_outputs), review: t(($) => $.worktree_lifecycle.resume_review),
    changes_requested: t(($) => $.worktree_lifecycle.rework), stale_review: t(($) => $.worktree_lifecycle.resume_review),
    active: t(($) => $.worktree_lifecycle.wait_run), no_target: t(($) => $.worktree_lifecycle.choose_target),
    unavailable: t(($) => $.worktree_lifecycle.reconnect), unowned: t(($) => $.worktree_lifecycle.reconnect),
    approved: t(($) => $.local_review.approved), merged: t(($) => $.local_review.merged), no_changes: t(($) => $.local_review.empty),
    multiple_reviews: t(($) => $.worktree_lifecycle.choose_target), retained: t(($) => $.worktree_lifecycle.check_task),
  };
  return <p className="break-words text-caption text-muted-foreground">{hints[reason] ?? <>{reason && <code>{reason} · </code>}{t(($) => $.worktree_lifecycle.check_task)}</>}</p>;
}

export function WorktreeRepositoryStatus({ repository }: { readonly repository: Lifecycle["repositoryDetails"][number] | undefined }) {
  const { t } = useT("issues");
  const reviewLabels: Record<string, string> = { draft: t(($) => $.local_review.draft), open: t(($) => $.local_review.state_open), approved: t(($) => $.local_review.approved), changes_requested: t(($) => $.local_review.changes_requested), merged: t(($) => $.local_review.merged) };
  return <div className="space-y-1 text-caption text-muted-foreground">
    <p className="break-all">{repository?.target ? t(($) => $.worktree_lifecycle.target, { target: repository.target }) : t(($) => $.worktree_lifecycle.choose_target)}{repository?.reviewState && ` · ${reviewLabels[repository.reviewState] ?? repository.reviewState}`}</p>
    {repository && <WorktreeActionLabel action={repository.nextAction} />}
    {repository?.reason && repository.reason !== repository.reviewState && <WorktreeReason reason={repository.reason} />}
  </div>;
}
