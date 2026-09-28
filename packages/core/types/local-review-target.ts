export function defaultReviewTarget(branches: readonly string[], requested = ""): string {
  return branches.includes(requested) ? requested : "";
}
