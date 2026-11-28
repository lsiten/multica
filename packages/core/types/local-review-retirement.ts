const retiredActions = new Set(["submit", "approve", "request_changes", "merge", "merge_selected", "stage", "unstage", "commit"]);

export function assertLocalReviewActionSupported(action: string | undefined): void {
  if (action && retiredActions.has(action)) {
    throw new Error("local_review_workflow_retired: Local MR writes are no longer supported. Use Git changes and issue acceptance.");
  }
}
