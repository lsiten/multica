import { useT } from "../../i18n";

export function useWorkProgressLabels() {
  const { t } = useT("projects");
  const reasons: Record<string, string> = {
    pending_me: t($ => $.work_progress.reasons.pending_me),
    pending_other: t($ => $.work_progress.reasons.pending_member),
    hierarchy_cycle: t($ => $.work_progress.reasons.hierarchy_cycle),
    dependency_cycle: t($ => $.work_progress.reasons.dependency_cycle),
    parent_closed_with_open_children: t($ => $.work_progress.reasons.parent_closed_with_open_children),
    cancelled_dependency: t($ => $.work_progress.reasons.cancelled_dependency),
    cancelled_children: t($ => $.work_progress.reasons.cancelled_children),
    cancelled_stage: t($ => $.work_progress.reasons.cancelled_stage),
    status_unknown: t($ => $.work_progress.reasons.status_unknown),
    dependency: t($ => $.work_progress.reasons.dependency),
    blocks_others: t($ => $.work_progress.reasons.blocks_others),
    explicit_block: t($ => $.work_progress.reasons.explicit_block),
    run_failed: t($ => $.work_progress.reasons.run_failed),
    runtime_offline: t($ => $.work_progress.reasons.runtime_offline),
    runtime_missing: t($ => $.work_progress.reasons.runtime_missing),
    assignee_unavailable: t($ => $.work_progress.reasons.assignee_unavailable),
    stalled: t($ => $.work_progress.reasons.stalled),
    run_ended_issue_open: t($ => $.work_progress.reasons.run_ended_issue_open),
    member_work: t($ => $.work_progress.reasons.member_work),
    next_step_recorded: t($ => $.work_progress.reasons.next_step_recorded),
    review: t($ => $.work_progress.reasons.review),
    parent_wrap_up: t($ => $.work_progress.reasons.parent_wrap_up),
    unassigned: t($ => $.work_progress.reasons.unassigned),
    stage_ready: t($ => $.work_progress.reasons.stage_ready),
    stage_waiting: t($ => $.work_progress.reasons.stage_waiting),
    ready: t($ => $.work_progress.reasons.ready),
    waiting_children: t($ => $.work_progress.reasons.waiting_children),
  };
  return { t, reasonLabel: (reason: string) => reasons[reason] ?? t($ => $.work_progress.reasons.unknown) };
}
