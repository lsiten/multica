"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { requirementOptions } from "@multica/core/issues/queries";
import type { Issue, Requirement } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

/**
 * Requirement marker + optional confirm/change lifecycle for a kind="requirement"
 * issue. The "R" marker renders from `issue.kind` with no network fetch, so a
 * board full of task cards stays cheap; the lifecycle pill (pending confirm /
 * confirmed / change needs re-confirm) is fetched only when `showLifecycle` is
 * set (issue detail). Renders nothing for ordinary (task) issues.
 */
export function RequirementBadge({
  issue,
  showLifecycle = false,
  className,
}: {
  issue: Issue;
  showLifecycle?: boolean;
  className?: string;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { data: requirement } = useQuery({
    ...requirementOptions(wsId, issue.id),
    enabled: showLifecycle && issue.kind === "requirement",
  });

  if (issue.kind !== "requirement") return null;

  return (
    <span className={cn("inline-flex shrink-0 items-center gap-1", className)}>
      <span
        title={t(($) => $.requirement.marker)}
        aria-label={t(($) => $.requirement.marker)}
        className="inline-flex items-center rounded-xs bg-accent px-1 text-[0.6rem] font-semibold text-accent-foreground"
      >
        {t(($) => $.requirement.marker_short)}
      </span>
      {showLifecycle && <RequirementLifecyclePill requirement={requirement} />}
    </span>
  );
}

/** Marks an ordinary task with no parent issue as a workspace-level task. */
export function TopLevelTaskBadge({ issue, className }: { issue: Issue; className?: string }) {
  const { t } = useT("issues");
  // `undefined` means an older/incomplete payload, not proof that the issue is
  // a root. Only the server's explicit null parent identifies a top-level task.
  if ((issue.kind ?? "task") === "requirement" || issue.parent_issue_id !== null) return null;

  return (
    <span className={cn("inline-flex shrink-0 items-center gap-1", className)}>
      <span
        title={t(($) => $.task_hierarchy.top_level)}
        aria-label={t(($) => $.task_hierarchy.top_level)}
        className="inline-flex items-center rounded-xs bg-muted px-1 text-[0.6rem] font-semibold text-muted-foreground"
      >
        {t(($) => $.task_hierarchy.top_level_short)}
      </span>
    </span>
  );
}

// A requirement may be pending confirm, confirmed, or confirmed-but-changed
// (a doc edit after confirmation re-opens the requirement). The "changed" state
// wins over "confirmed" because the un-confirmed change is the actionable fact.
function RequirementLifecyclePill({
  requirement,
}: {
  requirement: Requirement | null | undefined;
}) {
  const { t } = useT("issues");
  if (!requirement) return null;

  const changed = requirement.unconfirmed_change;
  const confirmed =
    requirement.lifecycle_status === "confirmed" || changed;
  const pending = !confirmed;

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-micro",
        pending
          ? "bg-warning/15 text-warning"
          : changed
            ? "bg-warning/15 text-warning"
            : "bg-success/15 text-success",
      )}
    >
      <span
        className={cn(
          "size-1.5 rounded-full",
          pending ? "bg-warning" : changed ? "bg-warning" : "bg-success",
        )}
      />
      <span className="truncate">
        {pending
          ? t(($) => $.requirement.pending_confirm)
          : changed
            ? t(($) => $.requirement.change_pending)
            : t(($) => $.requirement.confirmed)}
      </span>
    </span>
  );
}
