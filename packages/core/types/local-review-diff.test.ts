// @vitest-environment node
import { describe, expect, it } from "vitest";
import { reviewDiffLines } from "./local-review-diff";

describe("local review diff lines", () => {
  it("tracks separate old and new line numbers and resets for each hunk", () => {
    const rows = reviewDiffLines("--- a/app.ts\n+++ b/app.ts\n@@ -10,2 +20,2 @@\n-old\n+new\n same\n@@ -40 +50 @@\n-last\n+next\n");
    expect(rows[0]).toEqual({ text: "--- a/app.ts", kind: "header" });
    expect(rows[3]).toMatchObject({ kind: "remove", oldLine: 10 });
    expect(rows[4]).toMatchObject({ kind: "add", newLine: 20 });
    expect(rows[5]).toMatchObject({ oldLine: 11, newLine: 21 });
    expect(rows[7]).toMatchObject({ kind: "remove", oldLine: 40 });
    expect(rows[8]).toMatchObject({ kind: "add", newLine: 50 });
  });
  it("numbers untracked text but never labels a binary digest as added source", () => {
    expect(reviewDiffLines("hello\nworld\n", true)).toEqual([{ text: "hello", kind: "add", newLine: 1 }, { text: "world", kind: "add", newLine: 2 }]);
    expect(reviewDiffLines("Binary file SHA256: abc", true)[0]?.kind).toBe("header");
  });
});
