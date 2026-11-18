import type { Project } from "@multica/core/types";

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
