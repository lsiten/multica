export function defaultReviewTarget(branches: readonly string[]): string {
  return ["main", "master", "production", "test"].find((branch) => branches.includes(branch)) ?? branches[0] ?? "";
}
