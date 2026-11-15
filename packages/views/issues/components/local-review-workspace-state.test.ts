// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  parseReviewDiffLayout,
  parseReviewFileFilter,
  parseReviewFileView,
  reviewDirectoryGroups,
  reviewFileCode,
  reviewFileMatchesFilter,
  type ReviewFileRow,
} from "./local-review-workspace-state";

function file(path: string, status: string, oldPath?: string): ReviewFileRow {
  return { path, old_path: oldPath, status, preview: "text", additions: 1, deletions: 0 };
}

describe("local review workspace state", () => {
  it("falls back safely for unknown URL preferences", () => {
    expect(parseReviewDiffLayout("sideways")).toBe("unified");
    expect(parseReviewFileFilter("renamed")).toBe("all");
    expect(parseReviewFileView("cards")).toBe("list");
  });

  it("maps Git states to compact review labels", () => {
    expect(reviewFileCode(file("new.ts", "added"))).toBe("A");
    expect(reviewFileCode(file("old.ts", "deleted"))).toBe("D");
    expect(reviewFileCode(file("next.ts", "renamed", "before.ts"))).toBe("R");
    expect(reviewFileCode(file("changed.ts", "modified"))).toBe("M");
  });

  it("filters by path and review state without dropping search semantics", () => {
    const rows = [
      file("src/app.ts", "modified"),
      file("src/new.ts", "added"),
      file("docs/old.md", "deleted"),
    ];
    expect(rows.filter((row) => reviewFileMatchesFilter(row, "modified", "src"))).toEqual([rows[0]]);
    expect(rows.filter((row) => reviewFileMatchesFilter(row, "deleted", ""))).toEqual([rows[2]]);
  });

  it("groups directory siblings while preserving file order", () => {
    const rows = [file("src/a.ts", "modified"), file("README.md", "modified"), file("src/b.ts", "added")];
    expect(reviewDirectoryGroups(rows)).toEqual([
      { directory: "", files: [rows[1]] },
      { directory: "src", files: [rows[0], rows[2]] },
    ]);
  });
});
