import type { ProjectProgress, Project } from "@multica/core/types";

export function getProjectIssueMetrics(
  project: Pick<Project, "issue_count" | "done_count" | "leaf_issue_count" | "done_leaf_count" | "open_parent_count">,
) {
  return {
    totalCount: project.issue_count,
    completedCount: project.done_count,
    leafCount: project.leaf_issue_count ?? project.issue_count,
    completedLeafCount: project.done_leaf_count ?? project.done_count,
    openParentCount: project.open_parent_count ?? 0,
  };
}

/**
 * The project's progress summary. Servers that predate `progress` only send
 * issue_count / done_count, where done_count also counts cancelled issues;
 * that older ratio is the best available there.
 */
export function getProjectProgress(
  project: Pick<Project, "issue_count" | "done_count" | "progress">,
): ProjectProgress {
  if (project.progress) return project.progress;
  const scope = project.issue_count;
  const completed = Math.min(project.done_count, scope);
  return {
    unit: "issues",
    scope,
    completed,
    started: 0,
    unstarted: scope - completed,
    cancelled: 0,
    percent: scope > 0 ? Math.floor((completed * 100) / scope) : 0,
  };
}
