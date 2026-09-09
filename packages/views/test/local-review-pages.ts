import type { PagedReviewInput, ReviewFilePage, ReviewManifest } from "@multica/core/types/local-review-pages";

export function reviewManifestFixture(input: PagedReviewInput, filePath = "entry.ts"): ReviewManifest {
  const target = input.target || "main";
  const id = input.version_id || (input.path.includes("other") ? "d" : target === "main" ? "a" : target === "release" ? "b" : "c").repeat(64);
  return {
    version_id: id, runtime_id: input.runtime_id,
    header: { repository: input.path, branch: "feature", target, head: "1".repeat(40), target_head: "2".repeat(40), base: "3".repeat(40), dirty: false, committed: false },
    page: { files: [{ path: filePath, status: "modified", preview: "text", additions: 1, deletions: 1 }], total_files: 1, next_offset: 1, has_more: false, additions: 1, deletions: 1 },
    review: { snapshot_id: id, state: input.action === "approve" ? "approved" : input.action === "merge" ? "merged" : "open", comment: input.comment || "", merged_commit: input.action === "merge" ? "merged-sha" : "", events: [] },
  };
}

export function reviewFileFixture(input: PagedReviewInput, text = "+after"): ReviewFilePage {
  return { version_id: input.version_id || "a".repeat(64), path: input.file_path || "entry.ts", preview: "text", page: { lines: [{ text, kind: "add", new_line: 1 }], next_line: 1, has_more: false } };
}
