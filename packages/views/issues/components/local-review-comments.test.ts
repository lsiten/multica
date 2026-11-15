// @vitest-environment node

import { describe, expect, it } from "vitest";
import {
  appendLocalReviewReply,
  applyReviewFindingSuggestion,
  parseLocalReviewComments,
  reviewFindingsForPatch,
  type LocalReviewComment,
} from "./local-review-comments";

const comment: LocalReviewComment = {
  id: "src/app.ts:revision:new:12:body",
  file: "src/app.ts",
  side: "new",
  line: 12,
  revision: "revision",
  body: "body",
  resolved: false,
  author: "member",
  replies: [],
};

describe("local review comments", () => {
  it("restores valid durable comments and drops malformed entries", () => {
    expect(parseLocalReviewComments(JSON.stringify([
      comment,
      { ...comment, line: 0 },
      { ...comment, side: "sideways" },
      null,
    ]))).toEqual([comment]);
    expect(parseLocalReviewComments("{")).toEqual([]);
  });

  it("anchors agent findings to added lines with severity", () => {
    const findings = reviewFindingsForPatch({
      file: "src/app.ts",
      revision: "revision",
      page: {
        lines: [
          { kind: "context", text: "function run() {", old_line: 1, new_line: 1 },
          { kind: "add", text: "  debugger;", new_line: 2 },
        ],
        next_line: 2,
        has_more: false,
      },
    });

    expect(findings).toEqual([
      expect.objectContaining({
        author: "agent",
        file: "src/app.ts",
        line: 2,
        revision: "revision",
        side: "new",
        severity: "high",
        suggestion: {
          kind: "remove_line",
          label: "Remove line",
        },
      }),
    ]);
  });

  it("adds replies without losing the comment anchor", () => {
    const replies = appendLocalReviewReply([comment], comment.id, {
      id: "reply-1",
      body: "Handled upstream.",
      author: "member",
      createdAt: "2026-11-15T01:00:00Z",
    });

    expect(replies[0]?.replies).toEqual([
      expect.objectContaining({ id: "reply-1", body: "Handled upstream." }),
    ]);
    expect(replies[0]).toMatchObject({
      file: comment.file,
      line: comment.line,
      revision: comment.revision,
    });
  });

  it("applies a finding to exactly the anchored new-side line", () => {
    const finding: LocalReviewComment = {
      ...comment,
      author: "agent",
      line: 2,
      suggestion: { kind: "remove_line", label: "Remove line" },
    };

    expect(applyReviewFindingSuggestion("one\ndebugger;\nthree\n", finding)).toBe(
      "one\nthree\n",
    );
  });
});
