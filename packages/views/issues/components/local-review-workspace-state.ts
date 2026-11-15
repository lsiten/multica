import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import type { LocalIndexView } from "@multica/core/types/local-review-index";

export type ReviewDiffLayout = "split" | "unified";
export type ReviewFileFilter = "all" | "modified" | "added" | "deleted" | "conflicts";
export type ReviewFileView = "list" | "tree";

export function parseReviewDiffLayout(value: string | null): ReviewDiffLayout {
  return value === "split" ? "split" : "unified";
}

export function parseReviewFileFilter(value: string | null): ReviewFileFilter {
  return value === "modified" || value === "added" || value === "deleted" || value === "conflicts"
    ? value
    : "all";
}

export function parseReviewFileView(value: string | null): ReviewFileView {
  return value === "tree" ? "tree" : "list";
}

export type ReviewFileRow = ReviewManifest["page"]["files"][number] & {
  readonly status?: string;
  readonly indexFile?: LocalIndexView["status"]["files"][number];
};

export function reviewFileCode(file: ReviewFileRow): "M" | "A" | "D" | "R" | "U" {
  if (file.indexFile?.conflicted || file.status === "conflicted") return "U";
  if (file.indexFile?.untracked || file.status === "added" || file.status === "working_only") return "A";
  if (file.status === "deleted" || file.indexFile?.working_code === "D" || file.indexFile?.index_code === "D") return "D";
  if (file.old_path) return "R";
  return "M";
}

export function reviewFileMatchesFilter(
  file: ReviewFileRow,
  filter: ReviewFileFilter,
  query: string,
): boolean {
  if (query) {
    const needle = query.toLocaleLowerCase();
    if (!`${file.path} ${file.old_path ?? ""}`.toLocaleLowerCase().includes(needle)) return false;
  }
  if (filter === "all") return true;
  if (filter === "conflicts") return reviewFileCode(file) === "U";
  if (filter === "added") return reviewFileCode(file) === "A";
  if (filter === "deleted") return reviewFileCode(file) === "D";
  return ["M", "R"].includes(reviewFileCode(file));
}

export function reviewDirectoryGroups(
  files: readonly ReviewFileRow[],
): readonly { readonly directory: string; readonly files: readonly ReviewFileRow[] }[] {
  const groups = new Map<string, ReviewFileRow[]>();
  for (const file of files) {
    const slash = file.path.lastIndexOf("/");
    const directory = slash < 0 ? "" : file.path.slice(0, slash);
    groups.set(directory, [...(groups.get(directory) ?? []), file]);
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([directory, entries]) => ({ directory, files: entries }));
}
