import type { ReviewManifest } from "@multica/core/types/local-review-pages";

export type LocalReviewDecision = "submit" | "approve" | "request_changes" | "merge";
export type LocalReviewState = ReviewManifest["review"]["state"];

export const LOCAL_REVIEW_ACTIONS = {
  draft: ["submit"],
  open: ["request_changes", "approve"],
  approved: ["request_changes", "merge"],
  changes_requested: ["submit"],
  merged: [],
} as const satisfies Record<LocalReviewState, readonly LocalReviewDecision[]>;

export function localReviewActions(state: LocalReviewState): readonly LocalReviewDecision[] {
  return LOCAL_REVIEW_ACTIONS[state];
}
